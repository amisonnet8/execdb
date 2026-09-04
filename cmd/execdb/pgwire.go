package main

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/amisonnet8/execdb/engine"
)

// startPgwire starts the PostgreSQL wire protocol listeners opts
// configures (spec §8): a TCP listener (-p/--pg-addr) and/or a UNIX
// domain socket listener (-s/--socket), sharing one session
// implementation -- only the transport differs
// (.claude/rules/pgwire.md). The returned stop func is always safe to
// call, even if neither flag was set.
func startPgwire(db *engine.DB, opts *options) (stop func(), err error) {
	var listeners []net.Listener
	stop = func() {
		for _, ln := range listeners {
			ln.Close()
		}
		if opts.socket != "" {
			os.Remove(opts.socket)
		}
	}

	if opts.pgAddr == "" && opts.socket == "" {
		return stop, nil
	}

	if opts.pgAddr != "" {
		ln, err := net.Listen("tcp", opts.pgAddr)
		if err != nil {
			return stop, fmt.Errorf("pgwire: listen on %s: %w", opts.pgAddr, err)
		}
		listeners = append(listeners, ln)
	}
	if opts.socket != "" {
		os.Remove(opts.socket) // clear a stale socket left by a previous run
		ln, err := net.Listen("unix", opts.socket)
		if err != nil {
			stop()
			return stop, fmt.Errorf("pgwire: listen on %s: %w", opts.socket, err)
		}
		if err := os.Chmod(opts.socket, 0o600); err != nil {
			stop()
			return stop, fmt.Errorf("pgwire: chmod %s: %w", opts.socket, err)
		}
		listeners = append(listeners, ln)
	}

	for _, ln := range listeners {
		go acceptLoop(ln, db)
	}
	return stop, nil
}

func acceptLoop(ln net.Listener, db *engine.DB) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go handleConnection(conn, db)
	}
}

// handleConnection runs one client session: the authentication handshake
// (spec §8, Zero-Auth only in phase 1), then a loop of Simple Query /
// Terminate messages (.claude/rules/pgwire.md's protocol subset).
func handleConnection(conn net.Conn, db *engine.DB) {
	defer conn.Close()

	if !performHandshake(conn) {
		return
	}
	if err := sendStartupResponse(conn); err != nil {
		return
	}

	for {
		msg, err := readFrontendMessage(conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case 'Q':
			if err := handleSimpleQuery(conn, db, cStringFromBody(msg.Body)); err != nil {
				return
			}
			if err := writeReadyForQuery(conn, 'I'); err != nil {
				return
			}
		case 'X':
			return
		default:
			if writeErrorResponse(conn, sqlstateGeneric, fmt.Sprintf("unsupported message type %q", msg.Type)) != nil {
				return
			}
			if err := writeReadyForQuery(conn, 'I'); err != nil {
				return
			}
		}
	}
}

// performHandshake consumes SSLRequest/GSSENCRequest -- always answered
// with 'N' ("not supported, continue unencrypted"; phase 1 is Zero-Auth
// only, .claude/rules/pgwire.md) -- and then the actual StartupMessage,
// in whatever order the client sends them. libpq-based clients (psql
// included) send GSSENCRequest before SSLRequest when built with GSSAPI
// support; both must get 'N' or the client hangs waiting for a response
// that never comes. It reports whether the connection is still usable
// (false for a CancelRequest, which phase 1 does not act on, or an
// error; the caller just closes either way).
func performHandshake(conn net.Conn) bool {
	for {
		length, code, err := readStartupHeader(conn)
		if err != nil {
			return false
		}
		switch code {
		case codeSSLRequest, codeGSSENCRequest:
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return false
			}
		case codeCancelRequest:
			io.CopyN(io.Discard, conn, int64(length-8))
			return false
		case protocolVersion3:
			if _, err := readStartupParams(conn, int(length-8)); err != nil {
				return false
			}
			return true
		default:
			return false
		}
	}
}

func sendStartupResponse(conn net.Conn) error {
	if err := writeAuthenticationOk(conn); err != nil {
		return err
	}
	params := [][2]string{
		{"server_version", "13.0 (ExecDB " + version + ")"},
		{"client_encoding", "UTF8"},
		{"standard_conforming_strings", "on"},
		{"DateStyle", "ISO, MDY"},
		{"TimeZone", "UTC"},
		{"integer_datetimes", "on"},
	}
	for _, kv := range params {
		if err := writeParameterStatus(conn, kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := writeBackendKeyData(conn, 0, 0); err != nil {
		return err
	}
	return writeReadyForQuery(conn, 'I')
}

// handleSimpleQuery runs every statement in query in order (spec §8:
// Simple Query only). A returned error means the connection itself
// failed and the caller should give up on it; a SQL-level error is
// reported via ErrorResponse and simply stops the rest of this batch
// (matching real PostgreSQL, which does not run later statements in a
// multi-statement message after one fails).
func handleSimpleQuery(conn net.Conn, db *engine.DB, query string) error {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	if err := checkExternalAccess(query); err != nil {
		return writeErrorResponse(conn, sqlstateInsufficientPrivilege, err.Error())
	}
	for _, stmt := range splitStatements(query) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		ok, err := execOneStatement(conn, db, stmt)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}

func execOneStatement(conn net.Conn, db *engine.DB, stmt string) (ok bool, err error) {
	trimmed := strings.TrimSpace(stmt)
	if looksLikeRowReturning(trimmed) {
		rows, qerr := db.Query(stmt)
		if qerr != nil {
			return false, writeErrorResponse(conn, sqlstateGeneric, qerr.Error())
		}
		defer rows.Close()
		return sendRows(conn, rows)
	}

	result, qerr := db.Exec(stmt)
	if qerr != nil {
		return false, writeErrorResponse(conn, sqlstateGeneric, qerr.Error())
	}
	return true, writeCommandComplete(conn, commandTag(trimmed, result))
}

func sendRows(conn net.Conn, rows *sql.Rows) (ok bool, err error) {
	cols, cerr := rows.Columns()
	if cerr != nil {
		return false, writeErrorResponse(conn, sqlstateGeneric, cerr.Error())
	}
	if err := writeRowDescription(conn, cols); err != nil {
		return false, err
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	n := 0
	for rows.Next() {
		if serr := rows.Scan(ptrs...); serr != nil {
			return false, writeErrorResponse(conn, sqlstateGeneric, serr.Error())
		}
		fields := make([]*string, len(cols))
		for i, v := range vals {
			if v == nil {
				continue
			}
			s := formatValue(v)
			fields[i] = &s
		}
		if err := writeDataRow(conn, fields); err != nil {
			return false, err
		}
		n++
	}
	if rerr := rows.Err(); rerr != nil {
		return false, writeErrorResponse(conn, sqlstateGeneric, rerr.Error())
	}
	return true, writeCommandComplete(conn, fmt.Sprintf("SELECT %d", n))
}

// commandTag builds a PostgreSQL command tag for a non-row-returning
// statement. Real PostgreSQL's DML tags include a row count; TCL and
// anything else just echo the keyword, which is close enough for phase 1
// (the external I/F only ever reaches DML/TCL here, since checkExternalAccess
// has already rejected DDL and the "other" category).
func commandTag(stmt string, result sql.Result) string {
	kw := firstKeyword(stmt)
	n, _ := result.RowsAffected()
	switch kw {
	case "INSERT":
		return fmt.Sprintf("INSERT 0 %d", n)
	case "UPDATE":
		return fmt.Sprintf("UPDATE %d", n)
	case "DELETE":
		return fmt.Sprintf("DELETE %d", n)
	default:
		return kw
	}
}
