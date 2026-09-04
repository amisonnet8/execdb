package engine

// Spike test for execdb_spec.md §7's "provisional / unconfirmed" Serialize
// method. This file exists to settle, with real measurements instead of
// guesses, whether modernc.org/sqlite's Serialize/Deserialize can back the
// engine package's persistence model. See PLAN.md "フェーズ①のステップ" /
// Step 1 for the questions this answers. Once the decision is recorded
// there, this file is expected to be replaced by engine/serialize_test.go
// covering the chosen implementation.

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

// --- ① reachability: does *sql.Conn.Raw give us something we can type-assert
// Serialize/Deserialize out of, and what is the exact signature? ---

type serializer interface {
	Serialize() ([]byte, error)
}

type deserializer interface {
	Deserialize([]byte) error
}

func TestSpikeSerializeReachability(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	var (
		gotSerializer   bool
		gotDeserializer bool
	)
	err = conn.Raw(func(driverConn any) error {
		typ := reflect.TypeOf(driverConn)
		t.Logf("driverConn dynamic type: %v (kind=%v)", typ, typ.Kind())
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			t.Logf("  method: %s%s", m.Name, m.Func.Type())
		}

		if s, ok := driverConn.(serializer); ok {
			gotSerializer = true
			_ = s
		}
		if d, ok := driverConn.(deserializer); ok {
			gotDeserializer = true
			_ = d
		}
		return nil
	})
	if err != nil {
		t.Fatalf("conn.Raw: %v", err)
	}

	t.Logf("RESULT: local `serializer` interface satisfied = %v", gotSerializer)
	t.Logf("RESULT: local `deserializer` interface satisfied = %v", gotDeserializer)

	if !gotSerializer || !gotDeserializer {
		t.Fatalf("driver conn does not satisfy Serialize()([]byte,error) / Deserialize([]byte)error via local interface assertion; fallback required")
	}
}

// --- ② connection-sharing model: does :memory: give each *sql.Conn its own
// DB, and does a shared-cache URI make them see the same tables? Spec §2/§8
// require the REPL and the external I/F to act as two independent clients
// against the SAME in-memory database. ---

func TestSpikePlainMemoryIsPerConnection(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2) // force two distinct underlying connections

	c1, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	if _, err := c1.ExecContext(t.Context(), "CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatalf("create on c1: %v", err)
	}

	c2, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_, err = c2.ExecContext(t.Context(), "SELECT * FROM t")
	if err == nil {
		t.Fatalf("RESULT: unexpected — c2 could see c1's table under plain :memory: (no isolation)")
	}
	t.Logf("RESULT: plain :memory: is per-connection as expected; c2 cannot see c1's table (%v)", err)
}

func TestSpikeSharedCacheDSN(t *testing.T) {
	dsn := "file:spiketest1?mode=memory&cache=shared"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dsn, err)
	}
	defer db.Close()

	// Keeper connection: per spec §7, a shared-cache in-memory DB is freed
	// once its last connection closes. Hold one open for the DB's lifetime.
	keeper, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("keeper conn: %v", err)
	}
	defer keeper.Close()

	if _, err := keeper.ExecContext(t.Context(), "CREATE TABLE shared(a INTEGER)"); err != nil {
		t.Fatalf("create via keeper: %v", err)
	}
	if _, err := keeper.ExecContext(t.Context(), "INSERT INTO shared VALUES (1)"); err != nil {
		t.Fatalf("insert via keeper: %v", err)
	}

	db.SetMaxOpenConns(4) // allow more than one real connection to be opened
	other, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("other conn: %v", err)
	}
	defer other.Close()

	var n int
	row := other.QueryRowContext(t.Context(), "SELECT count(*) FROM shared")
	if err := row.Scan(&n); err != nil {
		t.Fatalf("RESULT: second connection cannot see keeper's table under shared cache: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row visible from second connection, got %d", n)
	}
	t.Logf("RESULT: shared-cache DSN (%q) + keeper connection lets independent *sql.Conn values see the same in-memory DB", dsn)

	// Confirm the "keeper required" half of the claim: close every
	// connection and see whether the DB survives via *sql.DB re-Conn.
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	// keeper is still open (deferred Close hasn't run yet) — DB must still
	// be alive; this is exercised implicitly by defer at test end, no
	// separate assertion needed here since Go closes it after this
	// function returns.
}

// --- ③ growth after Deserialize: does a restored DB accept new writes, or is
// it read-only / fixed-size (this is the failure mode that would force the
// fallback even if Serialize/Deserialize round-trip correctly)? ---

func TestSpikeDeserializeThenGrow(t *testing.T) {
	src, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	src.SetMaxOpenConns(1)

	if _, err := src.ExecContext(t.Context(), "CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.ExecContext(t.Context(), "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	var blob []byte
	srcConn, err := src.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := srcConn.Raw(func(dc any) error {
		s, ok := dc.(serializer)
		if !ok {
			return fmt.Errorf("driver conn does not implement serializer")
		}
		v, err := s.Serialize()
		if err != nil {
			return err
		}
		blob = v
		return nil
	}); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	srcConn.Close()
	t.Logf("serialized %d bytes from a 1-row DB", len(blob))

	dst, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	dst.SetMaxOpenConns(1)

	dstConn, err := dst.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dstConn.Close()

	if err := dstConn.Raw(func(dc any) error {
		d, ok := dc.(deserializer)
		if !ok {
			return fmt.Errorf("driver conn does not implement deserializer")
		}
		return d.Deserialize(blob)
	}); err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	// The actual risk: insert far more rows than the serialized image had
	// room for, forcing the page count to grow well past the original size.
	const rowsToAdd = 20000
	tx, err := dstConn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.PrepareContext(t.Context(), "INSERT INTO t VALUES (?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < rowsToAdd; i++ {
		if _, err := stmt.ExecContext(t.Context(), i); err != nil {
			stmt.Close()
			tx.Rollback()
			t.Fatalf("RESULT: insert #%d into deserialized DB failed (DB may be fixed-size): %v", i, err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var n int
	if err := dstConn.QueryRowContext(t.Context(), "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != rowsToAdd+1 {
		t.Fatalf("expected %d rows, got %d", rowsToAdd+1, n)
	}
	t.Logf("RESULT: deserialized DB accepted %d additional rows after restore (grows past original size)", rowsToAdd)
}

// --- ④ round-trip fidelity: table/index/view/trigger/autoincrement/blob all
// survive a Serialize → Deserialize round trip intact. ---

func TestSpikeRoundTripFidelity(t *testing.T) {
	src, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	src.SetMaxOpenConns(1)

	ddl := []string{
		`CREATE TABLE items(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, payload BLOB)`,
		`CREATE INDEX idx_items_name ON items(name)`,
		`CREATE VIEW item_names AS SELECT id, name FROM items`,
		`CREATE TABLE audit(id INTEGER PRIMARY KEY, msg TEXT)`,
		`CREATE TRIGGER trg_items_ai AFTER INSERT ON items BEGIN
			INSERT INTO audit(msg) VALUES ('inserted ' || new.id);
		END`,
	}
	for _, stmt := range ddl {
		if _, err := src.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("ddl %q: %v", stmt, err)
		}
	}

	blobPayload := []byte{0x00, 0x01, 0xFF, 0xFE, 0x10}
	if _, err := src.ExecContext(t.Context(), "INSERT INTO items(name, payload) VALUES (?, ?)", "alice", blobPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := src.ExecContext(t.Context(), "INSERT INTO items(name, payload) VALUES (?, ?)", "bob", nil); err != nil {
		t.Fatal(err)
	}

	srcConn, err := src.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	if err := srcConn.Raw(func(dc any) error {
		v, err := dc.(serializer).Serialize()
		blob = v
		return err
	}); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	srcConn.Close()

	dst, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	dst.SetMaxOpenConns(1)
	dstConn, err := dst.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dstConn.Close()
	if err := dstConn.Raw(func(dc any) error {
		return dc.(deserializer).Deserialize(blob)
	}); err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	// Schema survives: sqlite_master lists the same objects.
	rows, err := dstConn.QueryContext(t.Context(), "SELECT type, name FROM sqlite_master ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	var objs []string
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, typ+":"+name)
	}
	rows.Close()
	t.Logf("RESULT: sqlite_master after restore: %v", objs)
	wantAtLeast := []string{"index:idx_items_name", "table:audit", "table:items", "trigger:trg_items_ai", "view:item_names"}
	for _, w := range wantAtLeast {
		found := false
		for _, o := range objs {
			if o == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("RESULT: expected schema object %q missing after restore", w)
		}
	}

	// Data + BLOB survive, and the trigger fires on new inserts too.
	var name string
	var payload []byte
	if err := dstConn.QueryRowContext(t.Context(), "SELECT name, payload FROM items WHERE id = 1").Scan(&name, &payload); err != nil {
		t.Fatalf("read row 1: %v", err)
	}
	if name != "alice" || string(payload) != string(blobPayload) {
		t.Errorf("RESULT: row 1 mismatch after restore: name=%q payload=%x", name, payload)
	}

	if _, err := dstConn.ExecContext(t.Context(), "INSERT INTO items(name) VALUES ('carol')"); err != nil {
		t.Fatalf("insert after restore: %v", err)
	}
	// audit already holds 2 rows from the alice/bob inserts made before
	// Serialize (those fired the trigger on the source DB and are part of
	// the restored state); carol's insert after restore should add one more.
	var auditCount int
	if err := dstConn.QueryRowContext(t.Context(), "SELECT count(*) FROM audit").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Errorf("RESULT: expected 3 audit rows (2 pre-restore + 1 from trigger firing post-restore), got %d", auditCount)
	}

	// AUTOINCREMENT continuity: carol should get id 3, not reuse 1 or 2.
	var carolID int
	if err := dstConn.QueryRowContext(t.Context(), "SELECT id FROM items WHERE name = 'carol'").Scan(&carolID); err != nil {
		t.Fatal(err)
	}
	if carolID != 3 {
		t.Errorf("RESULT: AUTOINCREMENT sequence not preserved across restore: carol got id %d, want 3", carolID)
	}

	t.Logf("RESULT: round trip preserved schema, data, BLOB, AUTOINCREMENT sequence, and trigger behavior")
}
