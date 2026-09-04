package engine

import (
	"context"
	"testing"
)

// These cover the same ground as the (now removed) serialize_spike_test.go,
// but as ordinary regression tests against the confirmed implementation
// rather than exploratory spike checks. See PLAN.md's "Step 1で確定した
//事実" for the underlying measurements this locks in.

func TestSerializeDeserializeRoundTrip(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ddl := []string{
		`CREATE TABLE items(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, payload BLOB)`,
		`CREATE INDEX idx_items_name ON items(name)`,
		`CREATE VIEW item_names AS SELECT id, name FROM items`,
		`CREATE TRIGGER trg_items_ai AFTER INSERT ON items BEGIN
			INSERT INTO items(name) VALUES ('audit:' || new.id);
		END`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("ddl %q: %v", stmt, err)
		}
	}
	payload := []byte{0x00, 0x01, 0xFF}
	if _, err := db.Exec("INSERT INTO items(name, payload) VALUES (?, ?)", "alice", payload); err != nil {
		t.Fatal(err)
	}

	blob, err := db.serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("serialize returned an empty blob")
	}

	restored, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := deserializeInto(restored.keeper, blob); err != nil {
		t.Fatalf("deserializeInto: %v", err)
	}

	var name string
	var got []byte
	if err := restored.QueryRow("SELECT name, payload FROM items WHERE id = 1").Scan(&name, &got); err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if name != "alice" || string(got) != string(payload) {
		t.Errorf("row mismatch: name=%q payload=%x", name, got)
	}

	// The trigger should have fired once during the original insert, and
	// the schema (index/view/trigger) should have survived the round trip.
	var auditCount int
	if err := restored.QueryRow("SELECT count(*) FROM items WHERE name LIKE 'audit:%'").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Errorf("audit rows = %d, want 1 (trigger did not survive restore correctly)", auditCount)
	}

	// A restored database must accept new writes, not just reads (confirms
	// modernc.org/sqlite's SQLITE_DESERIALIZE_RESIZEABLE behavior).
	if _, err := restored.Exec("INSERT INTO items(name) VALUES ('bob')"); err != nil {
		t.Fatalf("insert after restore: %v", err)
	}
}

// TestDeserializeDoesNotPropagateToOtherConnections documents a real limit
// of modernc.org/sqlite's Deserialize discovered while implementing
// engine.DB: unlike plain SQL statements (see
// TestSharedCacheDSNIsVisibleToOtherConnections in engine_test.go),
// Deserialize only affects the exact connection it is called on -- not
// even a connection opened afterward on the same shared-cache DSN sees
// its result. This is why DB.Exec/Query/QueryRow route through the
// keeper connection instead of db.sdb's pool, and why Load builds an
// entirely new keeper rather than deserializing into the existing one.
func TestDeserializeDoesNotPropagateToOtherConnections(t *testing.T) {
	src, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec("INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	blob, err := src.serialize()
	if err != nil {
		t.Fatal(err)
	}

	sdb, keeper, err := newSharedCacheDB()
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	defer keeper.Close()

	if err := deserializeInto(keeper, blob); err != nil {
		t.Fatalf("deserializeInto: %v", err)
	}

	var n int
	if err := keeper.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("keeper itself should see its own Deserialize: %v", err)
	}

	other, err := sdb.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&n); err == nil {
		t.Error("expected a connection opened after Deserialize to NOT see its result " +
			"(if this now passes, modernc.org/sqlite's behavior has changed and the " +
			"Exec/Query/QueryRow keeper-routing workaround may no longer be necessary)")
	}
}
