package main

import (
	"context"
	"math"
	"testing"

	"github.com/amisonnet8/execdb/engine"
)

// pgtypeFixture builds a database covering the declared-type and
// expression/aggregate/literal cases the phase 4 Step 1 spike measured
// (PLAN.md's "フェーズ④Step 1で確定した事実"), so columnOID's mapping can
// be checked against real modernc.org/sqlite behavior rather than assumed.
func pgtypeFixture(t *testing.T) (*engine.DB, func()) {
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
		`CREATE TABLE t(
			i INTEGER, r REAL, tx TEXT, bl BLOB, num NUMERIC,
			vc VARCHAR(10), bo BOOLEAN, dt DATETIME
		)`,
		`INSERT INTO t VALUES (42, 3.14, 'hello', x'00ff', 1.5, 'abc', 1, '2026-01-01 00:00:00')`,
		`CREATE TABLE t2(a INTEGER)`,
		`INSERT INTO t2 VALUES ('affinity-violation')`,
	}
	for _, stmt := range stmts {
		if _, err := sess.Exec(stmt); err != nil {
			t.Fatalf("fixture setup %q: %v", stmt, err)
		}
	}
	return db, func() { sess.Close(); db.Close() }
}

func TestColumnOIDDeclaredTypes(t *testing.T) {
	db, cleanup := pgtypeFixture(t)
	defer cleanup()

	rows, err := db.Query(`SELECT i, r, tx, bl, num, vc, bo, dt FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]uint32{
		// "num" is declared NUMERIC, SQLite's catch-all affinity bucket:
		// columnOID falls back to the runtime Go type instead of a fixed
		// numeric(1700) OID (phase 4 Step 5's finding -- see columnOID's
		// doc comment), and the fixture's inserted value (1.5) scans as
		// float64.
		"i": oidInt8, "r": oidFloat8, "tx": oidText, "bl": oidBytea,
		"num": oidFloat8, "vc": oidText, "bo": oidBool, "dt": oidTimestamp,
	}
	for _, ct := range cts {
		if got := columnOID(ct); got != want[ct.Name()] {
			t.Errorf("columnOID(%s) = %d, want %d", ct.Name(), got, want[ct.Name()])
		}
	}
}

func TestColumnOIDExpressionColumns(t *testing.T) {
	db, cleanup := pgtypeFixture(t)
	defer cleanup()

	cases := []struct {
		query string
		want  uint32
	}{
		{`SELECT 1+1`, oidInt8},
		{`SELECT count(*) FROM t`, oidInt8},
		{`SELECT avg(i) FROM t`, oidFloat8},
		{`SELECT 'x'`, oidText},
		{`SELECT x'00ff'`, oidBytea},
		{`SELECT NULL`, oidText}, // no declared type, no sampled value: safe fallback
		{`SELECT i, tx FROM t WHERE 0`, oidInt8},
	}
	for _, c := range cases {
		rows, err := db.Query(c.query)
		if err != nil {
			t.Fatalf("query %q: %v", c.query, err)
		}
		cts, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if got := columnOID(cts[0]); got != c.want {
			t.Errorf("columnOID(%q) = %d, want %d", c.query, got, c.want)
		}
		rows.Close()
	}
}

func TestColumnOIDAffinityViolation(t *testing.T) {
	// A value that violates its column's declared INTEGER affinity: the
	// declared type still drives the OID (int8), even though the actual
	// scanned value is a string -- a documented limitation (PLAN.md), not
	// something pgEncodeValue silently "fixes".
	db, cleanup := pgtypeFixture(t)
	defer cleanup()

	rows, err := db.Query(`SELECT a FROM t2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if got := columnOID(cts[0]); got != oidInt8 {
		t.Errorf("columnOID = %d, want %d (declared type wins over actual value type)", got, oidInt8)
	}
}

func TestAffinityOID(t *testing.T) {
	cases := map[string]uint32{
		"INTEGER":     oidInt8,
		"INT":         oidInt8,
		"BIGINT":      oidInt8,
		"TEXT":        oidText,
		"VARCHAR(10)": oidText,
		"CLOB":        oidText,
		"BLOB":        oidBytea,
		"REAL":        oidFloat8,
		"FLOAT":       oidFloat8,
		"DOUBLE":      oidFloat8,
		"BOOLEAN":     oidBool,
		"BOOL":        oidBool,
		"DATE":        oidTimestamp,
		"DATETIME":    oidTimestamp,
		"TIMESTAMP":   oidTimestamp,
	}
	for decl, want := range cases {
		got, matched := affinityOID(decl)
		if !matched {
			t.Errorf("affinityOID(%q) matched = false, want true", decl)
			continue
		}
		if got != want {
			t.Errorf("affinityOID(%q) = %d, want %d", decl, got, want)
		}
	}
}

// TestAffinityOIDNumericCatchAll checks that SQLite's catch-all NUMERIC
// affinity (anything not matching INT/CHAR|CLOB|TEXT/BLOB/REAL|FLOA|DOUB,
// nor this package's own BOOL/DATE|TIME special cases) reports matched ==
// false, so columnOID falls back to sampling the runtime Go type instead
// of a fixed numeric(1700) OID (phase 4 Step 5's finding: real drivers
// request PostgreSQL's binary NUMERIC format, which is not implemented).
func TestAffinityOIDNumericCatchAll(t *testing.T) {
	for _, decl := range []string{"NUMERIC", "DECIMAL(10,2)", ""} {
		if _, matched := affinityOID(decl); matched {
			t.Errorf("affinityOID(%q) matched = true, want false (NUMERIC catch-all)", decl)
		}
	}
}

func TestPgEncodeValueText(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		v    any
		want *string
	}{
		{"nil is NULL", oidText, nil, nil},
		{"blob hex", oidBytea, []byte{0x00, 0xff}, strPtr(`\x00ff`)},
		{"float", oidFloat8, 3.5, strPtr("3.5")},
		{"NaN", oidFloat8, math.NaN(), strPtr("NaN")},
		{"+Inf", oidFloat8, math.Inf(1), strPtr("Infinity")},
		{"-Inf", oidFloat8, math.Inf(-1), strPtr("-Infinity")},
		{"int8 plain", oidInt8, int64(42), strPtr("42")},
		{"bool column int64 1", oidBool, int64(1), strPtr("t")},
		{"bool column int64 0", oidBool, int64(0), strPtr("f")},
		{"go bool true", oidBool, true, strPtr("t")},
		{"text", oidText, "hello", strPtr("hello")},
	}
	for _, c := range cases {
		got := pgEncodeValue(c.oid, false, c.v)
		if (got == nil) != (c.want == nil) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		if got != nil && *got != *c.want {
			t.Errorf("%s: got %q, want %q", c.name, *got, *c.want)
		}
	}
}

// TestPgEncodeValueBinary checks the binary wire encodings real pgx
// testing showed are required (phase 4 Step 5, PLAN.md): pgEncodeValue
// with useBinary must produce PostgreSQL's actual binary format for each
// of binaryCapableOIDs, not the text form.
func TestPgEncodeValueBinary(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		v    any
		want []byte
	}{
		{"int8", oidInt8, int64(42), []byte{0, 0, 0, 0, 0, 0, 0, 42}},
		{"int8 negative", oidInt8, int64(-1), []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"float8", oidFloat8, 1.0, []byte{0x3f, 0xf0, 0, 0, 0, 0, 0, 0}},
		{"bool true (go bool)", oidBool, true, []byte{1}},
		{"bool false (go bool)", oidBool, false, []byte{0}},
		{"bool true (sqlite int64 1)", oidBool, int64(1), []byte{1}},
		{"bool false (sqlite int64 0)", oidBool, int64(0), []byte{0}},
		{"bytea is raw bytes unchanged", oidBytea, []byte{0x00, 0xff, 0x10}, []byte{0x00, 0xff, 0x10}},
		{"timestamp at PostgreSQL epoch", oidTimestamp, pgEpoch, []byte{0, 0, 0, 0, 0, 0, 0, 0}},
	}
	for _, c := range cases {
		got := pgEncodeValue(c.oid, true, c.v)
		if got == nil {
			t.Errorf("%s: got nil, want binary data", c.name)
			continue
		}
		if string(c.want) != *got {
			t.Errorf("%s: got % x, want % x", c.name, []byte(*got), c.want)
		}
	}
}

// TestPgEncodeValueBinaryFallsBackToTextOnMismatch checks that requesting
// binary for a value whose actual Go type doesn't match what oid promised
// (an affinity violation, PLAN.md's phase 4 Step 1 notes) falls back to
// text instead of sending a malformed binary payload under a binary
// format code.
func TestPgEncodeValueBinaryFallsBackToTextOnMismatch(t *testing.T) {
	got := pgEncodeValue(oidInt8, true, "not-a-number")
	if got == nil || *got != "not-a-number" {
		t.Errorf("got %v, want text fallback %q", got, "not-a-number")
	}
}

// TestEncodeBinaryOnlyCoversBinaryCapableOIDs checks that oidText and the
// NUMERIC OID constant (never advertised by columnOID any more, but kept
// as a named constant) have no binary encoder.
func TestEncodeBinaryOnlyCoversBinaryCapableOIDs(t *testing.T) {
	for _, oid := range []uint32{oidText, oidNumeric} {
		if _, ok := encodeBinary(oid, "x"); ok {
			t.Errorf("encodeBinary(%d, ...) ok = true, want false (not binary-capable)", oid)
		}
	}
}

func strPtr(s string) *string { return &s }
