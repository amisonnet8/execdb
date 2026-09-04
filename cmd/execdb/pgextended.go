package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"

	"github.com/amisonnet8/execdb/engine"
)

// preparedStatement is one Parse'd SQL text (spec §8, Extended Query,
// phase 4 Step 5), reused across any number of Bind/Execute rounds via
// Session.PrepareContext's underlying *sql.Stmt (added in phase 3 Step 5
// for .import's bulk insert -- the same "prepare once, run many times"
// need Extended Query itself is built around).
type preparedStatement struct {
	sql          string
	stmt         *sql.Stmt
	numParams    int
	rowReturning bool
}

// portal is a Bind'd preparedStatement: a specific set of parameter values
// ready to Execute (real clients almost always Execute a portal exactly
// once, but nothing here assumes that). resultFormats is Bind's own
// per-column format-code request (interpreted by formatCodeFor's usual
// 0/1/N-codes rule), carried forward to whichever Describe(portal) or
// Execute call comes next -- both need it to decide binary vs. text per
// result column (buildResultColumns).
type portal struct {
	stmtName      string
	args          []any
	resultFormats []int16
}

// extendedQueryConn holds one pgwire connection's Extended Query state.
// "" is the unnamed statement/portal slot, always freely replaceable (a
// new Parse/Bind targeting "" silently discards whatever was there
// before, matching real PostgreSQL); a named statement/portal must not
// already exist. inError tracks the "skip messages until Sync" rule,
// which is distinct from txState's real SQL-transaction abort -- txState
// persists across Sync until an actual COMMIT/ROLLBACK, while inError
// resets at every Sync (pgwire.go's dispatch loop clears it there).
type extendedQueryConn struct {
	statements map[string]*preparedStatement
	portals    map[string]*portal
	inError    bool
}

func newExtendedQueryConn() *extendedQueryConn {
	return &extendedQueryConn{
		statements: make(map[string]*preparedStatement),
		portals:    make(map[string]*portal),
	}
}

// closeAll closes every prepared statement's *sql.Stmt still open when the
// connection ends (handleConnection defers this ahead of sess.Close(), so
// statements are released before their Session's connection is).
func (eq *extendedQueryConn) closeAll() {
	for _, ps := range eq.statements {
		ps.stmt.Close()
	}
}

// handleParse implements the Parse message: it prepares query on sess
// (spec §2's access control applies here, at parse time, exactly like
// Simple Query's checkExternalAccess) and stores the result under name.
// SQLite accepts Postgres-style "$1"/"$2" placeholders natively (phase 4
// Step 1's spike), so query needs no rewriting before being prepared.
func handleParse(ctx context.Context, w io.Writer, sess *engine.Session, eq *extendedQueryConn, body []byte) (ok bool, err error) {
	name, query, _, parsed := parseParseMessage(body)
	if !parsed {
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Parse message")
	}
	if aerr := checkExternalAccess(query); aerr != nil {
		return false, writeErrorResponse(w, sqlstateInsufficientPrivilege, aerr.Error())
	}
	if name != "" {
		if _, exists := eq.statements[name]; exists {
			return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("prepared statement %q already exists", name))
		}
	} else if old := eq.statements[""]; old != nil {
		old.stmt.Close()
	}

	stmt, perr := sess.PrepareContext(ctx, query)
	if perr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, perr.Error())
	}
	eq.statements[name] = &preparedStatement{
		sql:          query,
		stmt:         stmt,
		numParams:    countPlaceholders(query),
		rowReturning: looksLikeRowReturning(query),
	}
	return true, writeParseComplete(w)
}

// parseParseMessage decodes a Parse message body: cstring statement name,
// cstring query, Int16 parameter count, then that many Int32 parameter
// type OIDs. The OIDs themselves are read but discarded -- handleParse
// always lets SQLite infer parameter types dynamically at Bind/Execute
// time, the same way Simple Query already does for every value.
func parseParseMessage(body []byte) (stmtName, query string, paramOIDs []int32, ok bool) {
	stmtName, i, ok := readCString(body, 0)
	if !ok {
		return "", "", nil, false
	}
	query, i, ok = readCString(body, i)
	if !ok {
		return "", "", nil, false
	}
	if i+2 > len(body) {
		return "", "", nil, false
	}
	n := int(binary.BigEndian.Uint16(body[i : i+2]))
	i += 2
	paramOIDs = make([]int32, n)
	for j := 0; j < n; j++ {
		if i+4 > len(body) {
			return "", "", nil, false
		}
		paramOIDs[j] = int32(binary.BigEndian.Uint32(body[i : i+4]))
		i += 4
	}
	return stmtName, query, paramOIDs, true
}

// countPlaceholders returns the highest "$N" parameter index referenced in
// query outside of string/identifier literals and comments -- the same
// quote/comment-skipping approach access.go's hasReturningClause uses --
// which is how many parameters Bind must supply and Describe's
// ParameterDescription must declare.
func countPlaceholders(query string) int {
	runes := []rune(query)
	n := len(runes)
	i := 0
	maxIdx := 0
	for i < n {
		c := runes[i]
		switch {
		case c == '\'':
			i++
			for i < n {
				if runes[i] == '\'' {
					i++
					if i < n && runes[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
		case c == '"':
			i++
			for i < n && runes[i] != '"' {
				i++
			}
			if i < n {
				i++
			}
		case c == '-' && i+1 < n && runes[i+1] == '-':
			for i < n && runes[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && runes[i+1] == '*':
			i += 2
			for i+1 < n && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			} else {
				i = n
			}
		case c == '$' && i+1 < n && runes[i+1] >= '0' && runes[i+1] <= '9':
			j := i + 1
			for j < n && runes[j] >= '0' && runes[j] <= '9' {
				j++
			}
			if v, convErr := strconv.Atoi(string(runes[i+1 : j])); convErr == nil && v > maxIdx {
				maxIdx = v
			}
			i = j
		default:
			i++
		}
	}
	return maxIdx
}

// handleBind implements the Bind message: it decodes the parameter values
// bound to a prepared statement and stores the result as a named (or
// unnamed) portal, ready for Execute.
func handleBind(w io.Writer, eq *extendedQueryConn, body []byte) (ok bool, err error) {
	portalName, stmtName, paramFormats, paramValues, resultFormats, parsed := parseBindMessage(body)
	if !parsed {
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Bind message")
	}
	ps, exists := eq.statements[stmtName]
	if !exists {
		return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("prepared statement %q does not exist", stmtName))
	}
	if len(paramValues) != ps.numParams {
		return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf(
			"bind message supplies %d parameters, but prepared statement %q requires %d",
			len(paramValues), stmtName, ps.numParams))
	}
	for j := range paramValues {
		if formatCodeFor(paramFormats, j) == 1 {
			// ParameterDescription always advertises OID 0
			// (unspecified), so a well-behaved client sends text; a
			// client that sends binary anyway is rejected outright
			// rather than misinterpreted. Unlike result values
			// (buildResultColumns below), binary-format parameters are
			// not implemented -- real drivers tested in phase 4 Step 5
			// (PLAN.md) send text for parameters even when they request
			// binary results, so there was no observed need for it.
			return false, writeErrorResponse(w, sqlstateGeneric, "binary-format parameters are not supported")
		}
	}

	args := make([]any, len(paramValues))
	for j, v := range paramValues {
		if v == nil {
			args[j] = nil
		} else {
			args[j] = string(v)
		}
	}

	if portalName != "" {
		if _, exists := eq.portals[portalName]; exists {
			return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("portal %q already exists", portalName))
		}
	}
	eq.portals[portalName] = &portal{stmtName: stmtName, args: args, resultFormats: resultFormats}
	return true, writeBindComplete(w)
}

// parseBindMessage decodes a Bind message body: cstring portal name,
// cstring statement name, Int16 parameter-format-code count + that many
// Int16 format codes, Int16 parameter count + that many (Int32 length +
// bytes, or length -1 for SQL NULL), Int16 result-format-code count + that
// many Int16 result format codes (read but unused -- results are always
// sent in text format, matching Simple Query and RowDescription's own
// hardcoded format code 0).
func parseBindMessage(body []byte) (portalName, stmtName string, paramFormats []int16, paramValues [][]byte, resultFormats []int16, ok bool) {
	portalName, i, ok := readCString(body, 0)
	if !ok {
		return "", "", nil, nil, nil, false
	}
	stmtName, i, ok = readCString(body, i)
	if !ok {
		return "", "", nil, nil, nil, false
	}

	nFormats, i, ok := readInt16Count(body, i)
	if !ok {
		return "", "", nil, nil, nil, false
	}
	paramFormats = make([]int16, nFormats)
	for j := 0; j < nFormats; j++ {
		if i+2 > len(body) {
			return "", "", nil, nil, nil, false
		}
		paramFormats[j] = int16(binary.BigEndian.Uint16(body[i : i+2]))
		i += 2
	}

	nParams, i, ok := readInt16Count(body, i)
	if !ok {
		return "", "", nil, nil, nil, false
	}
	paramValues = make([][]byte, nParams)
	for j := 0; j < nParams; j++ {
		if i+4 > len(body) {
			return "", "", nil, nil, nil, false
		}
		plen := int32(binary.BigEndian.Uint32(body[i : i+4]))
		i += 4
		if plen < 0 {
			paramValues[j] = nil
			continue
		}
		if i+int(plen) > len(body) {
			return "", "", nil, nil, nil, false
		}
		paramValues[j] = body[i : i+int(plen)]
		i += int(plen)
	}

	nResultFormats, i, ok := readInt16Count(body, i)
	if !ok {
		return "", "", nil, nil, nil, false
	}
	resultFormats = make([]int16, nResultFormats)
	for j := 0; j < nResultFormats; j++ {
		if i+2 > len(body) {
			return "", "", nil, nil, nil, false
		}
		resultFormats[j] = int16(binary.BigEndian.Uint16(body[i : i+2]))
		i += 2
	}

	return portalName, stmtName, paramFormats, paramValues, resultFormats, true
}

func readInt16Count(body []byte, i int) (n, next int, ok bool) {
	if i+2 > len(body) {
		return 0, i, false
	}
	return int(binary.BigEndian.Uint16(body[i : i+2])), i + 2, true
}

// formatCodeFor returns the format code (0=text, 1=binary) that applies to
// parameter idx, per Bind's own encoding rule: zero format codes means
// text for every parameter, exactly one means that one code applies to
// all of them, and otherwise there is one code per parameter.
func formatCodeFor(formats []int16, idx int) int16 {
	switch len(formats) {
	case 0:
		return 0
	case 1:
		return formats[0]
	default:
		return formats[idx]
	}
}

// handleDescribe implements the Describe message for both of its targets:
// a prepared statement ('S', body[0]) answers with ParameterDescription
// followed by RowDescription/NoData; a portal ('P') answers with just
// RowDescription/NoData (real PostgreSQL never repeats parameter
// information for a portal, since it was already fixed at Bind time).
func handleDescribe(ctx context.Context, w io.Writer, sess *engine.Session, eq *extendedQueryConn, body []byte) (ok bool, err error) {
	if len(body) < 1 {
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Describe message")
	}
	kind := body[0]
	name, _, parsed := readCString(body, 1)
	if !parsed {
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Describe message")
	}

	switch kind {
	case 'S':
		ps, exists := eq.statements[name]
		if !exists {
			return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("prepared statement %q does not exist", name))
		}
		oids := make([]uint32, ps.numParams) // every parameter: 0 (unspecified) -- see writeParameterDescription
		if werr := writeParameterDescription(w, oids); werr != nil {
			return false, werr
		}
		// nil resultFormats: no Bind has happened yet for a
		// statement-level Describe, so there is no requested format to
		// honor -- real PostgreSQL always reports text here too.
		return describeRowShape(ctx, w, sess, ps, nil)
	case 'P':
		p, exists := eq.portals[name]
		if !exists {
			return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("portal %q does not exist", name))
		}
		ps, exists := eq.statements[p.stmtName]
		if !exists {
			return false, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("prepared statement %q does not exist", p.stmtName))
		}
		return describeRowShape(ctx, w, sess, ps, p.resultFormats)
	default:
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Describe message: unknown target")
	}
}

// describeRowShape answers a Describe's row-shape half (RowDescription or
// NoData) for ps. For a row-returning statement, it trial-executes ps with
// every placeholder bound to NULL, wrapped in a SAVEPOINT/ROLLBACK: phase
// 4 Step 1's spike found SQLite still reports each declared column's
// decltype-based ColumnTypes() correctly even with zero matching rows
// (PLAN.md's "フェーズ④Step 1で確定した事実"), which is exactly the
// column-shape information Describe needs -- without requiring the real
// parameter values Bind alone would provide, and without any new engine
// API. The SAVEPOINT is a safety net in case a statement looksLikeRowReturning
// missed (e.g. some RETURNING-clause variant) actually mutates data; it
// costs nothing when, as expected, the trial run only reads. resultFormats
// is nil for a statement-level Describe (no Bind has happened yet, so
// real PostgreSQL always reports text there) or a portal's Bind-supplied
// format-code request for a portal-level Describe (buildResultColumns).
func describeRowShape(ctx context.Context, w io.Writer, sess *engine.Session, ps *preparedStatement, resultFormats []int16) (ok bool, err error) {
	if !ps.rowReturning {
		return true, writeNoData(w)
	}

	if _, serr := sess.ExecContext(ctx, "SAVEPOINT execdb_describe"); serr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, serr.Error())
	}
	args := make([]any, ps.numParams)
	rows, qerr := ps.stmt.QueryContext(ctx, args...)
	rerr := rollbackDescribeSavepoint(ctx, sess)
	if qerr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, qerr.Error())
	}
	if rerr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, rerr.Error())
	}
	defer rows.Close()

	cts, cerr := rows.ColumnTypes()
	if cerr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, cerr.Error())
	}
	return true, writeRowDescription(w, buildResultColumns(cts, resultFormats))
}

// buildResultColumns builds the pgColumn slice for cts, deciding per
// column whether to use PostgreSQL's binary wire format: only when the
// client requested it (resultFormats, Bind's per-column format codes --
// nil means "always text", e.g. a statement-level Describe, which
// precedes any Bind) AND the column's OID has a binary encoder
// (pgtype.go's binaryCapableOIDs) -- confirmed via real pgx testing
// (phase 4 Step 5, PLAN.md) to be exactly the OIDs pgx's default mode
// itself requests binary for.
func buildResultColumns(cts []*sql.ColumnType, resultFormats []int16) []pgColumn {
	cols := make([]pgColumn, len(cts))
	for i, ct := range cts {
		oid := columnOID(ct)
		useBinary := formatCodeFor(resultFormats, i) == 1 && binaryCapableOIDs[oid]
		cols[i] = pgColumn{name: ct.Name(), oid: oid, binary: useBinary}
	}
	return cols
}

func rollbackDescribeSavepoint(ctx context.Context, sess *engine.Session) error {
	if _, err := sess.ExecContext(ctx, "ROLLBACK TO execdb_describe"); err != nil {
		return err
	}
	_, err := sess.ExecContext(ctx, "RELEASE execdb_describe")
	return err
}

// handleExecute runs the portal named in body (spec §8, Extended Query).
// Unlike Simple Query's execOneStatement, a successful row-returning
// Execute does NOT send its own RowDescription -- real PostgreSQL relies
// on whatever RowDescription a prior Describe already sent for this
// portal or its statement (sendDataRows, pgwire.go).
//
// The trailing Int32 maxRows in body is read but not honored: phase 4
// Step 5 always runs a portal to completion and sends CommandComplete
// rather than implementing PortalSuspended / row-limited fetching, since
// none of pgx/pgJDBC/node-postgres request row-limited execution by
// default (PLAN.md's phase 4 Step 5 notes) -- revisit if Step 7's real
// driver testing shows otherwise.
func handleExecute(ctx context.Context, w io.Writer, eq *extendedQueryConn, txState byte, body []byte) (ok bool, newTxState byte, err error) {
	portalName, _, parsed := readCString(body, 0)
	if !parsed {
		return false, txState, writeErrorResponse(w, sqlstateGeneric, "malformed Execute message")
	}

	p, exists := eq.portals[portalName]
	if !exists {
		return false, txState, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("portal %q does not exist", portalName))
	}
	ps, exists := eq.statements[p.stmtName]
	if !exists {
		return false, txState, writeErrorResponse(w, sqlstateGeneric, fmt.Sprintf("prepared statement %q does not exist", p.stmtName))
	}

	kw := firstKeyword(ps.sql)
	if txState == 'E' && kw != "COMMIT" && kw != "ROLLBACK" && kw != "END" {
		return false, txState, writeErrorResponse(w, sqlstateInFailedTransaction,
			"current transaction is aborted, commands ignored until end of transaction block")
	}

	if ps.rowReturning {
		rows, qerr := ps.stmt.QueryContext(ctx, p.args...)
		if qerr != nil {
			return false, txState, writeErrorResponse(w, sqlstateGeneric, qerr.Error())
		}
		defer rows.Close()
		cts, cerr := rows.ColumnTypes()
		if cerr != nil {
			return false, txState, writeErrorResponse(w, sqlstateGeneric, cerr.Error())
		}
		cols := buildResultColumns(cts, p.resultFormats)
		sok, werr := sendDataRows(w, rows, cols)
		if werr != nil {
			return false, txState, werr
		}
		if !sok {
			return false, txState, nil
		}
	} else {
		result, eerr := ps.stmt.ExecContext(ctx, p.args...)
		if eerr != nil {
			return false, txState, writeErrorResponse(w, sqlstateGeneric, eerr.Error())
		}
		if werr := writeCommandComplete(w, commandTag(ps.sql, result)); werr != nil {
			return false, txState, werr
		}
	}

	switch kw {
	case "BEGIN":
		txState = 'T'
	case "COMMIT", "ROLLBACK", "END":
		txState = 'I'
	}
	return true, txState, nil
}

// handleClose implements the Close message: it releases a named (or
// unnamed) prepared statement's *sql.Stmt or drops a portal. Closing
// something that does not exist is a no-op success, matching real
// PostgreSQL.
func handleClose(w io.Writer, eq *extendedQueryConn, body []byte) (ok bool, err error) {
	if len(body) < 1 {
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Close message")
	}
	kind := body[0]
	name, _, parsed := readCString(body, 1)
	if !parsed {
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Close message")
	}
	switch kind {
	case 'S':
		if ps, exists := eq.statements[name]; exists {
			ps.stmt.Close()
			delete(eq.statements, name)
		}
	case 'P':
		delete(eq.portals, name)
	default:
		return false, writeErrorResponse(w, sqlstateGeneric, "malformed Close message: unknown target")
	}
	return true, writeCloseComplete(w)
}
