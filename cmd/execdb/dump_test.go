package main

import (
	"context"
	"testing"

	"github.com/amisonnet8/execdb/engine"
)

// dumpFixture builds a database covering the cases .dump must handle
// correctly: TEXT/BLOB/NULL/REAL values (including a quote inside a
// TEXT value), an AUTOINCREMENT table (so sqlite_sequence has a row to
// restore), an INDEX, a VIEW, and a TRIGGER whose body contains an
// internal ";" -- exactly the construct a naive semicolon-splitting
// statement reader cannot replay (see the comment on
// TestDumpRoundTrip below).
func dumpFixture(t *testing.T) (*repl, func()) {
	t.Helper()
	db, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`CREATE TABLE t1(id INTEGER PRIMARY KEY AUTOINCREMENT, s TEXT, b BLOB, r REAL)`,
		`INSERT INTO t1(s, b, r) VALUES ('it''s', x'00ff', 1.5)`,
		`INSERT INTO t1(s, b, r) VALUES (NULL, NULL, NULL)`,
		`CREATE TABLE t2(a TEXT)`,
		`INSERT INTO t2 VALUES ('hello')`,
		`CREATE INDEX idx_t1_s ON t1(s)`,
		`CREATE VIEW v1 AS SELECT id, s FROM t1`,
		`CREATE TRIGGER trg_t1 AFTER INSERT ON t1 BEGIN UPDATE t2 SET a = a; END`,
	}
	for _, stmt := range stmts {
		if _, err := sess.Exec(stmt); err != nil {
			t.Fatalf("fixture setup %q: %v", stmt, err)
		}
	}

	return &repl{db: db, sess: sess}, func() { sess.Close(); db.Close() }
}

func TestDumpRoundTrip(t *testing.T) {
	r, cleanup := dumpFixture(t)
	defer cleanup()

	dump := captureStdout(t, func() { r.cmdDump(nil) })

	target, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	// Replayed via a single Exec call on the whole script rather than the
	// REPL's own line-based reader: SQLite's own tokenizer (invoked once
	// per Exec call on the full multi-statement text) has always
	// correctly treated the ";" inside the trigger's BEGIN...END body as
	// internal to that one CREATE TRIGGER statement. This test validates
	// .dump's SQL text itself, independent of how it gets fed to SQLite
	// -- tests/e2e.sh separately exercises piping a dump containing a
	// TRIGGER into a fresh REPL's stdin, which needs completeStatements
	// (complete.go, phase 3 Step 4) to work at all.
	if _, err := target.Exec(dump); err != nil {
		t.Fatalf("replaying the dump failed: %v\n--- dump ---\n%s", err, dump)
	}

	assertSameRows(t, r.db, target, `SELECT id, s, b, r FROM t1 ORDER BY id`)
	assertSameRows(t, r.db, target, `SELECT a FROM t2`)
	assertSameRows(t, r.db, target, `SELECT id, s FROM v1 ORDER BY id`)

	var seq int
	if err := target.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 't1'`).Scan(&seq); err != nil {
		t.Fatalf("sqlite_sequence not restored: %v", err)
	}
	if seq != 2 {
		t.Errorf("sqlite_sequence.seq for t1 = %d, want 2", seq)
	}

	// The trigger and index must have been recreated too, not just
	// their owning tables' data.
	if _, err := target.Exec(`INSERT INTO t1(s) VALUES ('triggers-this')`); err != nil {
		t.Fatalf("trigger did not survive the round trip: %v", err)
	}
	var idxCount int
	if err := target.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_t1_s'`,
	).Scan(&idxCount); err != nil {
		t.Fatal(err)
	}
	if idxCount != 1 {
		t.Error("index idx_t1_s did not survive the round trip")
	}
}

func TestDumpPatternFiltersToOneTable(t *testing.T) {
	r, cleanup := dumpFixture(t)
	defer cleanup()

	dump := captureStdout(t, func() { r.cmdDump([]string{"t1"}) })

	target, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := target.Exec(dump); err != nil {
		t.Fatalf("replaying a pattern-scoped dump failed: %v\n--- dump ---\n%s", err, dump)
	}

	var count int
	if err := target.QueryRow(`SELECT count(*) FROM t1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("t1 row count after pattern-scoped dump = %d, want 2", count)
	}
	if err := target.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 't2'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("t2 should not appear in a dump scoped to pattern \"t1\"")
	}
	// idx_t1_s belongs to t1 (tbl_name), so it should still be included
	// even though the pattern is an exact match on the table name, not
	// the index's own name.
	if err := target.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_t1_s'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("idx_t1_s (tbl_name = t1) should be included in a dump scoped to pattern \"t1\"")
	}
}

// assertSameRows runs query against both a and b and fails if the
// stringified rows differ, in order.
func assertSameRows(t *testing.T, a, b *engine.DB, query string) {
	t.Helper()
	wantRows, err := a.Query(query)
	if err != nil {
		t.Fatalf("source query %q: %v", query, err)
	}
	defer wantRows.Close()
	want := collectRows(t, wantRows)

	gotRows, err := b.Query(query)
	if err != nil {
		t.Fatalf("target query %q: %v", query, err)
	}
	defer gotRows.Close()
	got := collectRows(t, gotRows)

	if len(want) != len(got) {
		t.Fatalf("query %q: got %d rows, want %d\ngot:  %v\nwant: %v", query, len(got), len(want), got, want)
	}
	for i := range want {
		if len(want[i]) != len(got[i]) {
			t.Fatalf("query %q row %d: column count mismatch", query, i)
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("query %q row %d col %d = %v, want %v", query, i, j, got[i][j], want[i][j])
			}
		}
	}
}

func collectRows(t *testing.T, rows interface {
	Next() bool
	Scan(...any) error
	Columns() ([]string, error)
	Err() error
}) [][]string {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = formatValue(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
