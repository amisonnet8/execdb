package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/amisonnet8/execdb/engine"
)

// --- pure-function tests: message decoding, placeholder counting ---

func TestParseParseMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msgBuilderBody(&buf, func(b *msgBuilder) {
		b.cstring("stmt1")
		b.cstring("SELECT $1")
		b.int16(1)
		b.int32(0) // unspecified OID
	})

	name, query, oids, ok := parseParseMessage(buf.Bytes())
	if !ok {
		t.Fatal("expected parseParseMessage to succeed")
	}
	if name != "stmt1" || query != "SELECT $1" {
		t.Errorf("name=%q query=%q, want %q/%q", name, query, "stmt1", "SELECT $1")
	}
	if len(oids) != 1 || oids[0] != 0 {
		t.Errorf("oids = %v, want [0]", oids)
	}
}

func TestParseParseMessageMalformed(t *testing.T) {
	if _, _, _, ok := parseParseMessage([]byte{1, 2, 3}); ok {
		t.Error("expected malformed body to fail")
	}
}

func TestParseBindMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msgBuilderBody(&buf, func(b *msgBuilder) {
		b.cstring("portal1")
		b.cstring("stmt1")
		b.int16(0) // param format codes: all text
		b.int16(2) // 2 params
		b.int32(1)
		b.raw([]byte("1"))
		b.int32(-1) // NULL
		b.int16(0)  // result format codes: all text
	})

	portalName, stmtName, formats, values, resultFormats, ok := parseBindMessage(buf.Bytes())
	if !ok {
		t.Fatal("expected parseBindMessage to succeed")
	}
	if portalName != "portal1" || stmtName != "stmt1" {
		t.Errorf("portalName=%q stmtName=%q", portalName, stmtName)
	}
	if len(formats) != 0 {
		t.Errorf("formats = %v, want empty (all text)", formats)
	}
	if len(values) != 2 || string(values[0]) != "1" || values[1] != nil {
		t.Errorf("values = %v, want [\"1\" nil]", values)
	}
	if len(resultFormats) != 0 {
		t.Errorf("resultFormats = %v, want empty", resultFormats)
	}
}

func TestParseBindMessageMalformed(t *testing.T) {
	if _, _, _, _, _, ok := parseBindMessage([]byte{0}); ok {
		t.Error("expected malformed body to fail")
	}
}

func TestCountPlaceholders(t *testing.T) {
	cases := map[string]int{
		"SELECT 1":                           0,
		"SELECT $1":                          1,
		"SELECT $1, $2":                      2,
		"SELECT $2, $1":                      2, // highest index, not order of appearance
		"SELECT '$1 is not a placeholder'":   0,
		"SELECT 1 -- $1 in a comment\n":      0,
		"SELECT 1 /* $1 in a comment */":     0,
		`SELECT "$1 in a quoted identifier"`: 0,
	}
	for query, want := range cases {
		if got := countPlaceholders(query); got != want {
			t.Errorf("countPlaceholders(%q) = %d, want %d", query, got, want)
		}
	}
}

func TestFormatCodeFor(t *testing.T) {
	if got := formatCodeFor(nil, 0); got != 0 {
		t.Errorf("empty formats: got %d, want 0 (text)", got)
	}
	if got := formatCodeFor([]int16{1}, 5); got != 1 {
		t.Errorf("single format applies to all: got %d, want 1", got)
	}
	if got := formatCodeFor([]int16{0, 1, 0}, 1); got != 1 {
		t.Errorf("per-param format: got %d, want 1", got)
	}
}

// msgBuilderBody writes fn's built body directly into buf, without the
// outer message type/length framing writeMessage adds -- the tests above
// decode raw message bodies (what handleParse/handleBind's parse
// functions receive), not full wire messages.
func msgBuilderBody(buf *bytes.Buffer, fn func(*msgBuilder)) {
	b := &msgBuilder{}
	fn(b)
	buf.Write(b.buf)
}

// --- end-to-end tests: a real net.Pipe connection driven through
//     handleConnection, exercising the full Extended Query dispatch loop
//     in pgwire.go (inError/Sync skip rule, txState transitions) rather
//     than just the message-decoding helpers above. ---

func extendedQueryFixture(t *testing.T) *engine.DB {
	t.Helper()
	db, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE t(a INTEGER, b TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1, 'x'), (2, 'y')`); err != nil {
		t.Fatal(err)
	}
	return db
}

// startExtendedQueryConn runs handleConnection over an in-memory net.Pipe
// (client, server) against db, drains the startup handshake up through the
// first ReadyForQuery, and returns the client end ready for Extended Query
// messages. The returned stop func closes the client and waits for
// handleConnection to return.
func startExtendedQueryConn(t *testing.T, db *engine.DB) (client net.Conn, stop func()) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleConnection(server, db, "", "")
		close(done)
	}()

	sendStartupMessage(t, client, map[string]string{"user": "any", "database": "any"})
	drainUntilReadyForQuery(t, client)

	return client, func() {
		client.Close()
		<-done
	}
}

func sendStartupMessage(t *testing.T, w io.Writer, params map[string]string) {
	t.Helper()
	var body []byte
	for k, v := range params {
		body = append(body, k...)
		body = append(body, 0)
		body = append(body, v...)
		body = append(body, 0)
	}
	body = append(body, 0)
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(8+len(body)))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(protocolVersion3))
	if _, err := w.Write(hdr[:]); err != nil {
		t.Fatalf("write StartupMessage header: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write StartupMessage body: %v", err)
	}
}

// drainUntilReadyForQuery reads and discards messages until (and
// including) a ReadyForQuery, tolerating any number/order of
// AuthenticationOk/ParameterStatus/BackendKeyData in between -- exactly
// the startup handshake sequence sendStartupTail (pgwire.go) produces.
func drainUntilReadyForQuery(t *testing.T, r io.Reader) *frontendMessage {
	t.Helper()
	for {
		msg, err := readFrontendMessage(r)
		if err != nil {
			t.Fatalf("reading startup response: %v", err)
		}
		if msg.Type == 'Z' {
			return msg
		}
	}
}

func expectMessage(t *testing.T, r io.Reader, wantType byte) *frontendMessage {
	t.Helper()
	msg, err := readFrontendMessage(r)
	if err != nil {
		t.Fatalf("reading message (want %q): %v", wantType, err)
	}
	if msg.Type != wantType {
		t.Fatalf("message type = %q, want %q (body: %q)", msg.Type, wantType, msg.Body)
	}
	return msg
}

func sendParse(t *testing.T, w io.Writer, name, query string) {
	t.Helper()
	if err := writeMessage(w, 'P', func(b *msgBuilder) {
		b.cstring(name)
		b.cstring(query)
		b.int16(0)
	}); err != nil {
		t.Errorf("send Parse: %v", err)
	}
}

// sendParseWithOIDs is sendParse plus an explicit parameter-type-OID list,
// for tests that need a client to declare real types the way pgJDBC does
// (TestExtendedQueryAcceptsBinaryFormatParametersWithDeclaredOID below) --
// sendParse always sends zero parameter types, which is fine for every
// other test since handleParse never uses them beyond storing them.
func sendParseWithOIDs(t *testing.T, w io.Writer, name, query string, oids []uint32) {
	t.Helper()
	if err := writeMessage(w, 'P', func(b *msgBuilder) {
		b.cstring(name)
		b.cstring(query)
		b.int16(int16(len(oids)))
		for _, oid := range oids {
			b.int32(int32(oid))
		}
	}); err != nil {
		t.Errorf("send Parse: %v", err)
	}
}

func sendBind(t *testing.T, w io.Writer, portal, stmt string, values []string, nulls []bool) {
	t.Helper()
	if err := writeMessage(w, 'B', func(b *msgBuilder) {
		b.cstring(portal)
		b.cstring(stmt)
		b.int16(0) // param format codes: all text
		b.int16(int16(len(values)))
		for i, v := range values {
			if nulls != nil && nulls[i] {
				b.int32(-1)
				continue
			}
			b.int32(int32(len(v)))
			b.raw([]byte(v))
		}
		b.int16(0) // result format codes: all text
	}); err != nil {
		t.Errorf("send Bind: %v", err)
	}
}

func sendDescribe(t *testing.T, w io.Writer, kind byte, name string) {
	t.Helper()
	if err := writeMessage(w, 'D', func(b *msgBuilder) {
		b.byte(kind)
		b.cstring(name)
	}); err != nil {
		t.Errorf("send Describe: %v", err)
	}
}

func sendExecute(t *testing.T, w io.Writer, portal string) {
	t.Helper()
	if err := writeMessage(w, 'E', func(b *msgBuilder) {
		b.cstring(portal)
		b.int32(0) // maxRows: unlimited
	}); err != nil {
		t.Errorf("send Execute: %v", err)
	}
}

func sendSync(t *testing.T, w io.Writer) {
	t.Helper()
	if err := writeMessage(w, 'S', func(b *msgBuilder) {}); err != nil {
		t.Errorf("send Sync: %v", err)
	}
}

func sendClose(t *testing.T, w io.Writer, kind byte, name string) {
	t.Helper()
	if err := writeMessage(w, 'C', func(b *msgBuilder) {
		b.byte(kind)
		b.cstring(name)
	}); err != nil {
		t.Errorf("send Close: %v", err)
	}
}

// sendPipelined runs fn (a sequence of sendX calls) on its own goroutine
// and returns immediately. This is required whenever a test sends more
// than one request before reading the first response: unlike a real TCP
// socket's OS-level send buffer, net.Pipe (used throughout this file) is
// fully synchronous and unbuffered, so a Write blocks until a matching
// Read consumes it -- if the test goroutine itself kept writing a second
// request without first reading the first request's response, and the
// server (which writes its response before reading the next message) is
// simultaneously blocked trying to write that response, both sides
// deadlock. Running the writes on a separate goroutine while the test
// goroutine reads responses avoids that rendezvous deadlock and,
// incidentally, exercises genuine Extended Query pipelining.
func sendPipelined(fn func()) {
	go fn()
}

func TestExtendedQueryFullRoundTrip(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "", "SELECT a, b FROM t WHERE a = $1")
		sendBind(t, client, "", "", []string{"1"}, nil)
		sendDescribe(t, client, 'P', "")
		sendExecute(t, client, "")
		sendSync(t, client)
	})

	expectMessage(t, client, '1') // ParseComplete
	expectMessage(t, client, '2') // BindComplete
	rd := expectMessage(t, client, 'T')
	if !bytes.Contains(rd.Body, []byte("a\x00")) || !bytes.Contains(rd.Body, []byte("b\x00")) {
		t.Errorf("RowDescription missing expected column names: %q", rd.Body)
	}
	dr := expectMessage(t, client, 'D')
	if !bytes.Contains(dr.Body, []byte("x")) {
		t.Errorf("DataRow missing expected value %q: %q", "x", dr.Body)
	}
	cc := expectMessage(t, client, 'C')
	if !bytes.HasPrefix(cc.Body, []byte("SELECT 1\x00")) {
		t.Errorf("CommandComplete = %q, want prefix %q", cc.Body, "SELECT 1\x00")
	}
	rfq := expectMessage(t, client, 'Z')
	if rfq.Body[0] != 'I' {
		t.Errorf("ReadyForQuery status = %q, want 'I'", rfq.Body[0])
	}
}

func TestExtendedQueryStatementDescribeReturnsParameterDescription(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "s1", "SELECT a FROM t WHERE a = $1")
		sendDescribe(t, client, 'S', "s1")
		sendSync(t, client)
	})

	expectMessage(t, client, '1') // ParseComplete
	pd := expectMessage(t, client, 't')
	numParams := binary.BigEndian.Uint16(pd.Body[0:2])
	if numParams != 1 {
		t.Errorf("ParameterDescription count = %d, want 1", numParams)
	}
	expectMessage(t, client, 'T') // RowDescription: SELECT is row-returning
	expectMessage(t, client, 'Z')
}

func TestExtendedQueryDescribeNonRowReturningIsNoData(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "s1", "INSERT INTO t VALUES ($1, $2)")
		sendDescribe(t, client, 'S', "s1")
		sendSync(t, client)
	})

	expectMessage(t, client, '1')
	expectMessage(t, client, 't') // ParameterDescription (2 params)
	expectMessage(t, client, 'n') // NoData: INSERT has no result columns
	expectMessage(t, client, 'Z')
}

func TestExtendedQueryNamedStatementReusedAcrossBinds(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "s1", "SELECT b FROM t WHERE a = $1")
		sendSync(t, client)
	})
	expectMessage(t, client, '1')
	expectMessage(t, client, 'Z')

	for _, tc := range []struct{ arg, want string }{{"1", "x"}, {"2", "y"}} {
		sendPipelined(func() {
			sendBind(t, client, "p", "s1", []string{tc.arg}, nil)
			sendExecute(t, client, "p")
			sendClose(t, client, 'P', "p")
			sendSync(t, client)
		})

		expectMessage(t, client, '2') // BindComplete
		dr := expectMessage(t, client, 'D')
		if !bytes.Contains(dr.Body, []byte(tc.want)) {
			t.Errorf("arg %q: DataRow = %q, want to contain %q", tc.arg, dr.Body, tc.want)
		}
		expectMessage(t, client, 'C') // CommandComplete
		expectMessage(t, client, '3') // CloseComplete
		expectMessage(t, client, 'Z')
	}
}

func TestExtendedQueryErrorSkipsToSync(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	// Parse a statement that references a nonexistent table -- Parse
	// itself doesn't validate against the schema (SQLite only does that
	// at prepare time, which handleParse does perform via
	// sess.PrepareContext), so this should fail right at Parse.
	sendPipelined(func() {
		sendParse(t, client, "", "SELECT * FROM nonexistent_table")
		// These must all be silently skipped (no response at all) per
		// the "ignore everything until Sync" rule.
		sendBind(t, client, "", "", nil, nil)
		sendDescribe(t, client, 'P', "")
		sendExecute(t, client, "")
		sendSync(t, client)
	})

	errMsg := expectMessage(t, client, 'E') // ErrorResponse from the failed Parse
	if !bytes.Contains(errMsg.Body, []byte("nonexistent_table")) {
		t.Errorf("ErrorResponse body = %q, want it to mention the missing table", errMsg.Body)
	}
	rfq := expectMessage(t, client, 'Z') // Sync's response: nothing else in between
	if rfq.Body[0] != 'I' {
		t.Errorf("ReadyForQuery status = %q, want 'I' (no transaction was open)", rfq.Body[0])
	}

	// A fresh Parse after Sync must work normally again.
	sendPipelined(func() {
		sendParse(t, client, "", "SELECT 1")
		sendSync(t, client)
	})
	expectMessage(t, client, '1')
	expectMessage(t, client, 'Z')
}

func TestExtendedQueryAbortsTransactionOnError(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "begin", "BEGIN")
		sendBind(t, client, "pb", "begin", nil, nil)
		sendExecute(t, client, "pb")
		sendSync(t, client)
	})
	expectMessage(t, client, '1')
	expectMessage(t, client, '2')
	expectMessage(t, client, 'C')
	rfq := expectMessage(t, client, 'Z')
	if rfq.Body[0] != 'T' {
		t.Fatalf("ReadyForQuery status after BEGIN = %q, want 'T'", rfq.Body[0])
	}

	sendPipelined(func() {
		sendParse(t, client, "", "SELECT * FROM nonexistent_table")
		sendSync(t, client)
	})
	expectMessage(t, client, 'E')
	rfq = expectMessage(t, client, 'Z')
	if rfq.Body[0] != 'E' {
		t.Fatalf("ReadyForQuery status after a failed statement inside BEGIN = %q, want 'E'", rfq.Body[0])
	}

	// A later Execute must be rejected with 25P02 while still in 'E',
	// even though it is not itself malformed.
	sendPipelined(func() {
		sendParse(t, client, "s2", "SELECT 1")
		sendBind(t, client, "p2", "s2", nil, nil)
		sendExecute(t, client, "p2")
		sendSync(t, client)
	})
	expectMessage(t, client, '1') // Parse/Bind still succeed outside a query
	expectMessage(t, client, '2')
	errMsg := expectMessage(t, client, 'E')
	if !bytes.Contains(errMsg.Body, []byte("25P02")) {
		t.Errorf("expected SQLSTATE 25P02, got %q", errMsg.Body)
	}
	rfq = expectMessage(t, client, 'Z')
	if rfq.Body[0] != 'E' {
		t.Fatalf("ReadyForQuery status = %q, want still 'E'", rfq.Body[0])
	}

	sendPipelined(func() {
		sendParse(t, client, "rb", "ROLLBACK")
		sendBind(t, client, "pr", "rb", nil, nil)
		sendExecute(t, client, "pr")
		sendSync(t, client)
	})
	expectMessage(t, client, '1')
	expectMessage(t, client, '2')
	expectMessage(t, client, 'C')
	rfq = expectMessage(t, client, 'Z')
	if rfq.Body[0] != 'I' {
		t.Errorf("ReadyForQuery status after ROLLBACK = %q, want 'I'", rfq.Body[0])
	}
}

func TestExtendedQueryCloseRemovesStatement(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "s1", "SELECT 1")
		sendClose(t, client, 'S', "s1")
		sendSync(t, client)
	})
	expectMessage(t, client, '1')
	expectMessage(t, client, '3') // CloseComplete
	expectMessage(t, client, 'Z')

	sendPipelined(func() {
		sendBind(t, client, "", "s1", nil, nil)
		sendSync(t, client)
	})
	errMsg := expectMessage(t, client, 'E')
	if !bytes.Contains(errMsg.Body, []byte("s1")) {
		t.Errorf("expected an error naming the closed statement, got %q", errMsg.Body)
	}
	expectMessage(t, client, 'Z')
}

// TestExtendedQueryRejectsBinaryFormatParameters checks the case
// decodeBinaryParam (pgtype.go) cannot possibly handle: a client that
// Binds a parameter in binary format without having declared any type for
// it in Parse (sendParse always sends zero parameter types), leaving
// handleBind with OID 0 (unspecified) and no way to know how to decode the
// bytes. This is now a narrower case than the name suggests -- a client
// that DOES declare a real OID in Parse can Bind that parameter in binary,
// see TestExtendedQueryAcceptsBinaryFormatParametersWithDeclaredOID below
// (phase 4 Step 7, PLAN.md).
func TestExtendedQueryRejectsBinaryFormatParameters(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParse(t, client, "s1", "SELECT a FROM t WHERE a = $1")
		sendSync(t, client)
	})
	expectMessage(t, client, '1')
	expectMessage(t, client, 'Z')

	sendPipelined(func() {
		if err := writeMessage(client, 'B', func(b *msgBuilder) {
			b.cstring("")
			b.cstring("s1")
			b.int16(1) // one format code
			b.int16(1) // binary
			b.int16(1) // 1 param
			b.int32(4)
			b.raw([]byte{0, 0, 0, 1})
			b.int16(0)
		}); err != nil {
			t.Errorf("send Bind: %v", err)
		}
		sendSync(t, client)
	})

	errMsg := expectMessage(t, client, 'E')
	if !bytes.Contains(errMsg.Body, []byte("binary")) {
		t.Errorf("expected an error mentioning binary format, got %q", errMsg.Body)
	}
	expectMessage(t, client, 'Z')
}

// TestExtendedQueryAcceptsBinaryFormatParametersWithDeclaredOID is the
// regression test for a real interoperability bug phase 4 Step 7 found via
// actual pgJDBC connection testing (PLAN.md's "フェーズ④Step 7" notes): a
// client's own Parse message can declare a concrete parameter type OID --
// pgJDBC does this by default for a plain PreparedStatement.setInt,
// declaring OID 23 (int4) -- and then Bind that parameter in binary
// format, entirely independent of what ParameterDescription answered back
// (which always advertises 0/unspecified, handleDescribe). Before this
// step, ExecDB rejected every binary-format parameter outright, which made
// prepared statements unusable from pgJDBC's own default configuration.
func TestExtendedQueryAcceptsBinaryFormatParametersWithDeclaredOID(t *testing.T) {
	db := extendedQueryFixture(t)
	client, stop := startExtendedQueryConn(t, db)
	defer stop()

	sendPipelined(func() {
		sendParseWithOIDs(t, client, "s1", "SELECT b FROM t WHERE a = $1", []uint32{oidInt4})
		sendSync(t, client)
	})
	expectMessage(t, client, '1') // ParseComplete
	expectMessage(t, client, 'Z')

	sendPipelined(func() {
		if err := writeMessage(client, 'B', func(b *msgBuilder) {
			b.cstring("p1")
			b.cstring("s1")
			b.int16(1)                // one format code
			b.int16(1)                // binary
			b.int16(1)                // 1 param
			b.int32(4)                // int4 is 4 bytes wide
			b.raw([]byte{0, 0, 0, 1}) // big-endian int4 value 1
			b.int16(0)                // result formats: text
		}); err != nil {
			t.Errorf("send Bind: %v", err)
		}
		sendDescribe(t, client, 'P', "p1")
		sendExecute(t, client, "p1")
		sendSync(t, client)
	})

	expectMessage(t, client, '2') // BindComplete
	expectMessage(t, client, 'T') // RowDescription
	dr := expectMessage(t, client, 'D')
	if !bytes.Contains(dr.Body, []byte("x")) {
		t.Errorf("expected the row for a=1 (b='x'), got %q", dr.Body)
	}
	expectMessage(t, client, 'C') // CommandComplete
	expectMessage(t, client, 'Z')
}
