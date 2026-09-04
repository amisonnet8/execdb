package main

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"testing"

	"github.com/amisonnet8/execdb/engine"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn wrote. printRows and its helpers write straight to
// os.Stdout (matching the rest of the REPL's query-result output,
// .claude/rules/cli-output.md), so this is the only way to test them
// without threading an io.Writer through every renderer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wp

	fn()

	wp.Close()
	os.Stdout = old
	out, err := io.ReadAll(rp)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// fixtureRows opens a fresh in-memory DB, seeds one table covering NULL,
// a quote-containing string, a BLOB, an integer, and a real, and returns
// the *sql.Rows for "SELECT * FROM t ORDER BY id" plus a cleanup func.
func fixtureRows(t *testing.T) (*sql.Rows, func()) {
	t.Helper()
	db, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t(id INTEGER PRIMARY KEY, s TEXT, n INTEGER, r REAL, b BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1, 'it''s', 42, 1.5, x'00ff')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (2, NULL, NULL, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id, s, n, r, b FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	return rows, func() { rows.Close(); db.Close() }
}

func TestPrintRowsList(t *testing.T) {
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeList}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := "1|it's|42|1.5|\x00\xff\n2||||\n"
	if got != want {
		t.Errorf("list mode:\ngot  %q\nwant %q", got, want)
	}
}

func TestPrintRowsListWithHeaders(t *testing.T) {
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeList, headers: true}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := "id|s|n|r|b\n1|it's|42|1.5|\x00\xff\n2||||\n"
	if got != want {
		t.Errorf("list+headers mode:\ngot  %q\nwant %q", got, want)
	}
}

func TestPrintRowsCSV(t *testing.T) {
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeCSV, headers: true}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := "id,s,n,r,b\r\n" +
		"1,it's,42,1.5,\x00\xff\r\n" +
		"2,,,,\r\n"
	if got != want {
		t.Errorf("csv mode:\ngot  %q\nwant %q", got, want)
	}
}

func TestPrintRowsLine(t *testing.T) {
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeLine}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := "id = 1\n s = it's\n n = 42\n r = 1.5\n b = \x00\xff\n\n" +
		"id = 2\n s = \n n = \n r = \n b = \n\n"
	if got != want {
		t.Errorf("line mode:\ngot  %q\nwant %q", got, want)
	}
}

func TestPrintRowsColumn(t *testing.T) {
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeColumn, headers: true}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := "id  s     n   r    b\n" +
		"--  ----  --  ---  --\n" +
		"1   it's  42  1.5  \x00\xff\n" +
		"2                  \n"
	if got != want {
		t.Errorf("column mode:\ngot  %q\nwant %q", got, want)
	}
}

func TestPrintRowsColumnWithoutHeaders(t *testing.T) {
	// .mode column does not force headers back on if the user explicitly
	// turned them off afterward.
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeColumn, headers: false}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := "1   it's  42  1.5  \x00\xff\n" +
		"2                  \n"
	if got != want {
		t.Errorf("column mode without headers:\ngot  %q\nwant %q", got, want)
	}
}

func TestPrintRowsJSON(t *testing.T) {
	rows, cleanup := fixtureRows(t)
	defer cleanup()

	r := &repl{mode: modeJSON}
	got := captureStdout(t, func() { r.printRows(rows) })

	want := `[{"id":1,"s":"it's","n":42,"r":1.5,"b":"00ff"},{"id":2,"s":null,"n":null,"r":null,"b":null}]` + "\n"
	if got != want {
		t.Errorf("json mode:\ngot  %q\nwant %q", got, want)
	}
}

func TestCmdModeValidatesArgument(t *testing.T) {
	r := &repl{mode: modeList}
	stderr := captureStderr(t, func() { r.cmdMode([]string{"bogus"}) })
	if r.mode != modeList {
		t.Errorf("mode changed to %q after an invalid .mode argument", r.mode)
	}
	if stderr == "" {
		t.Error("expected an error message on an invalid .mode argument")
	}
}

func TestCmdModeColumnTurnsHeadersOn(t *testing.T) {
	r := &repl{mode: modeList, headers: false}
	r.cmdMode([]string{"column"})
	if r.mode != modeColumn {
		t.Errorf("mode = %q, want %q", r.mode, modeColumn)
	}
	if !r.headers {
		t.Error("switching to column mode should turn headers on")
	}
}

func TestCmdHeaders(t *testing.T) {
	r := &repl{}
	r.cmdHeaders([]string{"on"})
	if !r.headers {
		t.Error(".headers on should set headers = true")
	}
	r.cmdHeaders([]string{"off"})
	if r.headers {
		t.Error(".headers off should set headers = false")
	}
}

// captureStderr mirrors captureStdout for functions that report errors to
// stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = wp

	fn()

	wp.Close()
	os.Stderr = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rp); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
