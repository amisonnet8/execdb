package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

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
		go acceptLoop(ln, db, opts.user, opts.password)
	}
	return stop, nil
}

func acceptLoop(ln net.Listener, db *engine.DB, user, password string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go handleConnection(conn, db, user, password)
	}
}

// handleConnection runs one client session: the authentication handshake
// (spec §8 -- Zero-Auth by default, or a cleartext password challenge when
// user != "", auth.go's authenticateConnection), then a loop of Simple
// Query / Extended Query / Terminate messages
// (.claude/rules/pgwire.md's protocol subset).
//
// One TCP/UDS connection gets exactly one engine.Session (spec §2/§8: the
// external I/F is an independent client of the live database, with its
// own real SQL transactions -- see engine/session.go). The Session's
// context is canceled when this connection ends, including when the
// client disconnects while a query is still running (watchForDisconnect
// below) or a separate connection sends a matching CancelRequest
// (globalCancelRegistry, pgcancel.go, phase 4 Step 6) -- two independent
// cancellation triggers on the same context.
func handleConnection(conn net.Conn, db *engine.DB, user, password string) {
	defer conn.Close()

	startupParams, ok := performHandshake(conn)
	if !ok {
		return
	}
	if !authenticateConnection(conn, user, password, startupParams) {
		return
	}

	key, cc, unregister := globalCancelRegistry.register()
	defer unregister()

	if err := sendStartupTail(conn, key); err != nil {
		return
	}

	sess, err := db.Session(context.Background())
	if err != nil {
		writeErrorResponse(conn, sqlstateGeneric, err.Error())
		return
	}
	defer sess.Close()

	params := newSessionParams(startupParameterStatus())
	eq := newExtendedQueryConn()
	defer eq.closeAll()
	txState := byte('I')
	for {
		msg, err := readFrontendMessage(conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case 'Q':
			// A fresh, single-query context.CancelFunc is registered with
			// cc (both watchForDisconnect below and any concurrent
			// CancelRequest, pgcancel.go, call through it) and explicitly
			// unregistered right after this query finishes -- not
			// deferred, since this case body runs inside the connection's
			// long-lived for loop, and a defer here would only fire when
			// the whole connection closes, not at the end of this one
			// query (connCancel's doc comment).
			ctx, cancel := context.WithCancel(context.Background())
			end := cc.begin(cancel)
			stop := watchForDisconnect(conn, cancel)
			txState, err = handleSimpleQuery(ctx, conn, sess, params, txState, cStringFromBody(msg.Body))
			stop()
			end()
			cancel()
			if err != nil {
				return
			}
			if err := writeReadyForQuery(conn, txState); err != nil {
				return
			}

		// Extended Query (spec §8, phase 4 Step 5). Unlike 'Q' above,
		// none of these use watchForDisconnect: a client may legitimately
		// pipeline several of these messages back-to-back without
		// waiting for a response to each one, so a background Read
		// racing the main loop for the same bytes could mistake a
		// pipelined message for a disconnect and cancel a query that is
		// still perfectly healthy. The tradeoff (documented in
		// PLAN.md's phase 4 Step 5 notes) is that a client vanishing
		// mid-Execute without closing the TCP connection is only
		// noticed the next time this loop tries to read from conn, not
		// while that Execute is still running. A CancelRequest
		// (pgcancel.go) still works here, since it goes through cc
		// rather than a disconnect-detecting background read.
		case 'P', 'B', 'D', 'E', 'H':
			if eq.inError {
				continue // real PostgreSQL's rule: skip to the next Sync
			}
			ctx, cancel := context.WithCancel(context.Background())
			end := cc.begin(cancel)
			var ok bool
			switch msg.Type {
			case 'P':
				ok, err = handleParse(ctx, conn, sess, eq, msg.Body)
			case 'B':
				ok, err = handleBind(conn, eq, msg.Body)
			case 'D':
				ok, err = handleDescribe(ctx, conn, sess, eq, msg.Body)
			case 'E':
				ok, txState, err = handleExecute(ctx, conn, eq, txState, msg.Body)
			case 'H':
				ok = true // Flush: a no-op, since responses are already sent as they're produced
			}
			end()
			cancel()
			if err != nil {
				return
			}
			if !ok {
				eq.inError = true
				if txState == 'T' {
					txState = 'E'
				}
			}
		case 'C':
			if eq.inError {
				continue
			}
			ok, cerr := handleClose(conn, eq, msg.Body)
			if cerr != nil {
				return
			}
			if !ok {
				eq.inError = true
				if txState == 'T' {
					txState = 'E'
				}
			}
		case 'S':
			eq.inError = false
			if err := writeReadyForQuery(conn, txState); err != nil {
				return
			}

		case 'X':
			return
		default:
			if writeErrorResponse(conn, sqlstateGeneric, fmt.Sprintf("unsupported message type %q", msg.Type)) != nil {
				return
			}
			if err := writeReadyForQuery(conn, txState); err != nil {
				return
			}
		}
	}
}

// watchForDisconnect starts watching conn for the peer disconnecting
// while the caller is busy running a query (during which the caller
// itself is not reading from conn), and calls cancel if that happens.
// The caller must call the returned stop before resuming its own reads
// from conn, or the two would race for the same bytes.
//
// Simple Query is a strict request/response protocol: the client will
// not send another message on this connection until it receives this
// query's response, so a Read here should stay blocked with nothing
// arriving for as long as the query legitimately runs. If that Read
// returns instead -- an error (the peer closed the connection) or
// unexpected data (a protocol violation) -- something is wrong and
// canceling ctx is the safest reaction, since it stops an in-progress
// query from continuing to run for a client that is no longer listening.
func watchForDisconnect(conn net.Conn, cancel context.CancelFunc) (stop func()) {
	stopped := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		var buf [1]byte
		conn.Read(buf[:])
		select {
		case <-stopped:
			// stop() below force-unblocked this Read via SetReadDeadline;
			// the query simply finished normally.
		default:
			cancel()
		}
	}()
	return func() {
		close(stopped)
		conn.SetReadDeadline(time.Now())
		<-watcherDone
		conn.SetReadDeadline(time.Time{})
	}
}

// performHandshake consumes SSLRequest/GSSENCRequest -- always answered
// with 'N' ("not supported, continue unencrypted"; TLS is out of scope,
// .claude/rules/pgwire.md) -- and then the actual StartupMessage, in
// whatever order the client sends them. libpq-based clients (psql
// included) send GSSENCRequest before SSLRequest when built with GSSAPI
// support; both must get 'N' or the client hangs waiting for a response
// that never comes. A CancelRequest (phase 4 Step 6) is looked up in
// globalCancelRegistry and, if it names a still-open connection, cancels
// it; either way this connection itself is reported unusable, since real
// PostgreSQL never sends any response to a CancelRequest and always
// closes that connection immediately (the caller does the same here). On
// success it returns the StartupMessage's parameters (notably "user",
// which auth.go's authenticateConnection checks against --user) and
// whether the connection is still usable.
func performHandshake(conn net.Conn) (map[string]string, bool) {
	for {
		length, code, err := readStartupHeader(conn)
		if err != nil {
			return nil, false
		}
		switch code {
		case codeSSLRequest, codeGSSENCRequest:
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return nil, false
			}
		case codeCancelRequest:
			handleCancelRequest(conn, length)
			return nil, false
		case protocolVersion3:
			params, err := readStartupParams(conn, int(length-8))
			if err != nil {
				return nil, false
			}
			return params, true
		default:
			return nil, false
		}
	}
}

// startupParameterStatus is the list of ParameterStatus values ExecDB
// reports to every client at connection time (spec §8's handshake). It
// also seeds that connection's sessionParams (pgsession.go's
// newSessionParams), so a SHOW before any SET reports the same value the
// client was already told at startup.
func startupParameterStatus() [][2]string {
	return [][2]string{
		{"server_version", "13.0 (ExecDB " + version + ")"},
		{"client_encoding", "UTF8"},
		{"standard_conforming_strings", "on"},
		{"DateStyle", "ISO, MDY"},
		{"TimeZone", "UTC"},
		{"integer_datetimes", "on"},
	}
}

// sendStartupTail sends the fixed sequence that follows successful
// authentication (spec §8's handshake, after auth.go's
// authenticateConnection has already sent AuthenticationOk): a
// ParameterStatus for each of startupParameterStatus's values,
// BackendKeyData (carrying key, so a later CancelRequest naming it can
// find this connection in globalCancelRegistry -- pgcancel.go, phase 4
// Step 6), then ReadyForQuery.
func sendStartupTail(conn net.Conn, key cancelKey) error {
	for _, kv := range startupParameterStatus() {
		if err := writeParameterStatus(conn, kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := writeBackendKeyData(conn, key.pid, key.secret); err != nil {
		return err
	}
	return writeReadyForQuery(conn, 'I')
}

// handleSimpleQuery runs every statement in query in order (spec §8:
// Simple Query only), tracking transaction state across the connection
// the way real PostgreSQL's ReadyForQuery status byte does: txState is
// 'I' (idle), 'T' (inside a transaction), or 'E' (inside a transaction
// that hit an error and can no longer execute anything but
// COMMIT/ROLLBACK). SET/SHOW are intercepted (handleSessionCommand,
// pgsession.go) before reaching SQLite and leave txState unchanged. It
// returns the state after processing query.
//
// A returned error means the connection itself failed (including its
// context being canceled -- e.g. by watchForDisconnect) and the caller
// should give up on it; a SQL-level error is reported via ErrorResponse
// and simply stops the rest of this batch (matching real PostgreSQL,
// which does not run later statements in a multi-statement message after
// one fails).
func handleSimpleQuery(ctx context.Context, conn net.Conn, sess *engine.Session, params sessionParams, txState byte, query string) (byte, error) {
	if strings.TrimSpace(query) == "" {
		return txState, nil
	}
	if err := checkExternalAccess(query); err != nil {
		if werr := writeErrorResponse(conn, sqlstateInsufficientPrivilege, err.Error()); werr != nil {
			return txState, werr
		}
		if txState == 'T' {
			return 'E', nil
		}
		return txState, nil
	}

	for _, stmt := range splitStatements(query) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		kw := firstKeyword(trimmed)

		if txState == 'E' && kw != "COMMIT" && kw != "ROLLBACK" && kw != "END" {
			if err := writeErrorResponse(conn, sqlstateInFailedTransaction,
				"current transaction is aborted, commands ignored until end of transaction block"); err != nil {
				return txState, err
			}
			return txState, nil
		}

		if handled, err := handleSessionCommand(conn, params, kw, trimmed); handled {
			if err != nil {
				return txState, err
			}
			continue // SET/SHOW leave txState unchanged, like real PostgreSQL
		}

		ok, err := execOneStatement(ctx, conn, sess, stmt)
		if err != nil {
			return txState, err
		}
		if !ok {
			if txState == 'T' {
				txState = 'E'
			}
			return txState, nil
		}

		switch kw {
		case "BEGIN":
			txState = 'T'
		case "COMMIT", "ROLLBACK", "END":
			txState = 'I'
		}
	}
	return txState, nil
}

func execOneStatement(ctx context.Context, conn net.Conn, sess *engine.Session, stmt string) (ok bool, err error) {
	trimmed := strings.TrimSpace(stmt)
	if looksLikeRowReturning(trimmed) {
		rows, qerr := sess.QueryContext(ctx, stmt)
		if qerr != nil {
			return false, writeErrorResponse(conn, sqlstateGeneric, qerr.Error())
		}
		defer rows.Close()
		return sendRows(conn, rows)
	}

	result, qerr := sess.ExecContext(ctx, stmt)
	if qerr != nil {
		return false, writeErrorResponse(conn, sqlstateGeneric, qerr.Error())
	}
	return true, writeCommandComplete(conn, commandTag(trimmed, result))
}

// sendRows sends rows' RowDescription (built from its live column types,
// spec §8's type mapping) followed by every DataRow and a CommandComplete
// (sendDataRows). Used by Simple Query (execOneStatement above), which
// always announces a fresh RowDescription for each query.
func sendRows(w io.Writer, rows *sql.Rows) (ok bool, err error) {
	cts, cerr := rows.ColumnTypes()
	if cerr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, cerr.Error())
	}
	cols := make([]pgColumn, len(cts))
	for i, ct := range cts {
		cols[i] = pgColumn{name: ct.Name(), oid: columnOID(ct)}
	}
	if err := writeRowDescription(w, cols); err != nil {
		return false, err
	}
	return sendDataRows(w, rows, cols)
}

// sendDataRows writes rows' remaining DataRows (each value encoded per
// cols' OID via pgEncodeValue) followed by a CommandComplete("SELECT n")
// tag. Split out from sendRows so pgextended.go's Execute can reuse it
// without resending a RowDescription: real PostgreSQL's Execute only ever
// emits DataRow/CommandComplete, relying on whatever RowDescription a
// prior Describe already sent for the portal (or its statement).
func sendDataRows(w io.Writer, rows *sql.Rows, cols []pgColumn) (ok bool, err error) {
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	n := 0
	for rows.Next() {
		if serr := rows.Scan(ptrs...); serr != nil {
			return false, writeErrorResponse(w, sqlstateGeneric, serr.Error())
		}
		fields := make([]*string, len(cols))
		for i, v := range vals {
			fields[i] = pgEncodeValue(cols[i].oid, cols[i].binary, v)
		}
		if err := writeDataRow(w, fields); err != nil {
			return false, err
		}
		n++
	}
	if rerr := rows.Err(); rerr != nil {
		return false, writeErrorResponse(w, sqlstateGeneric, rerr.Error())
	}
	return true, writeCommandComplete(w, fmt.Sprintf("SELECT %d", n))
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
