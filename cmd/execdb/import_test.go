package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amisonnet8/execdb/engine"
)

func importFixture(t *testing.T) (*repl, func()) {
	t.Helper()
	db, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return &repl{db: db, sess: sess}, func() { sess.Close(); db.Close() }
}

func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportCreatesTableFromHeader(t *testing.T) {
	r, cleanup := importFixture(t)
	defer cleanup()

	path := writeCSV(t, "a,b\n1,hello\n2,world\n")
	stderr := captureStderr(t, func() { r.cmdImport([]string{path, "t"}) })
	if got := stderr; got == "" {
		t.Error("expected a confirmation message on stderr")
	}

	var count int
	if err := r.sess.QueryRow(`SELECT count(*) FROM t`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2", count)
	}

	var a, b string
	if err := r.sess.QueryRow(`SELECT a, b FROM t ORDER BY a`).Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	if a != "1" || b != "hello" {
		t.Errorf("got (%q, %q), want (\"1\", \"hello\")", a, b)
	}

	// All-TEXT columns, named from the header row.
	cols, err := r.tableColumns("t")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || cols[0] != "a" || cols[1] != "b" {
		t.Errorf("columns = %v, want [a b]", cols)
	}
}

func TestImportQuotedAndMultilineFields(t *testing.T) {
	r, cleanup := importFixture(t)
	defer cleanup()

	path := writeCSV(t, "a,b\n1,\"quoted, value\"\n2,\"line1\nline2\"\n")
	captureStderr(t, func() { r.cmdImport([]string{path, "t"}) })

	var b string
	if err := r.sess.QueryRow(`SELECT b FROM t WHERE a = '1'`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if b != "quoted, value" {
		t.Errorf("got %q, want %q", b, "quoted, value")
	}
	if err := r.sess.QueryRow(`SELECT b FROM t WHERE a = '2'`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if b != "line1\nline2" {
		t.Errorf("got %q, want %q", b, "line1\nline2")
	}
}

func TestImportIntoExistingTableTreatsFirstLineAsData(t *testing.T) {
	r, cleanup := importFixture(t)
	defer cleanup()

	if _, err := r.sess.Exec(`CREATE TABLE t(x, y)`); err != nil {
		t.Fatal(err)
	}
	path := writeCSV(t, "1,hello\n2,world\n")
	captureStderr(t, func() { r.cmdImport([]string{path, "t"}) })

	var count int
	if err := r.sess.QueryRow(`SELECT count(*) FROM t`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2 (no header line should have been skipped)", count)
	}
}

func TestImportFieldCountMismatchRollsBackEverything(t *testing.T) {
	r, cleanup := importFixture(t)
	defer cleanup()

	path := writeCSV(t, "a,b\n1,hello\n2\n3,world\n")
	stderr := captureStderr(t, func() { r.cmdImport([]string{path, "t"}) })
	if stderr == "" {
		t.Fatal("expected an error message on stderr")
	}

	var count int
	if err := r.sess.QueryRow(`SELECT count(*) FROM t`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("row count = %d, want 0 -- a mismatched row should roll back the whole import, not just skip that row", count)
	}
}

func TestImportEmptyFileIsAnErrorForANewTable(t *testing.T) {
	r, cleanup := importFixture(t)
	defer cleanup()

	path := writeCSV(t, "")
	stderr := captureStderr(t, func() { r.cmdImport([]string{path, "t"}) })
	if stderr == "" {
		t.Fatal("expected an error message on stderr")
	}

	exists, err := r.tableExists("t")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("no table should have been created from an empty file")
	}
}

func TestImportWithinAnOpenTransactionDoesNotBreakIt(t *testing.T) {
	r, cleanup := importFixture(t)
	defer cleanup()

	if _, err := r.sess.Exec(`CREATE TABLE outer_t(a)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.sess.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.sess.Exec(`INSERT INTO outer_t VALUES ('user-row')`); err != nil {
		t.Fatal(err)
	}

	path := writeCSV(t, "a,b\n1,hello\n")
	captureStderr(t, func() { r.cmdImport([]string{path, "t"}) })

	if _, err := r.sess.Exec(`COMMIT`); err != nil {
		t.Fatalf("the user's own transaction should still be open and committable after .import: %v", err)
	}

	var count int
	if err := r.sess.QueryRow(`SELECT count(*) FROM outer_t`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("outer_t row count = %d, want 1", count)
	}
	if err := r.sess.QueryRow(`SELECT count(*) FROM t`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("t row count = %d, want 1", count)
	}
}
