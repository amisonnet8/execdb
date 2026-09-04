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

	blob, err := db.serializeBarrier()
	if err != nil {
		t.Fatalf("serializeBarrier: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("serializeBarrier returned an empty blob")
	}

	restored, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	// Goes through the same loadBlobInto path Open/Load use in production,
	// not a bare deserializeInto(restored.keeper, ...): restored.QueryRow
	// below reads through the connection pool (db.sdb), and Deserialize's
	// result is invisible to any connection but the one it ran on
	// (TestDeserializeDoesNotPropagateToOtherConnections below).
	if err := loadBlobInto(blob, restored.dsn); err != nil {
		t.Fatalf("loadBlobInto: %v", err)
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
// of modernc.org/sqlite's Deserialize, and is why loadBlobInto exists
// (serialize.go) instead of deserializing directly into DB's live
// database: unlike plain SQL statements (see
// TestPooledConnectionsShareTheLiveDatabase in engine_test.go),
// Deserialize only affects the exact connection it is called on -- not
// even a connection opened afterward on the same DSN sees its result.
// Internally, Deserialize reopens its target schema as an anonymous
// (unshared) memdb store, which is why no choice of DSN fixes this
// (.claude/rules/sqlite-quirks.md). loadBlobInto instead deserializes
// into a throwaway connection and then uses SQLite's online Backup API
// (backup.go) to copy that into the live database through the normal
// btree/pager machinery, which every connection on it can see.
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
	blob, err := src.serializeBarrier()
	if err != nil {
		t.Fatal(err)
	}

	sdb, keeper, _, err := newLiveDB()
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
			"backup-based loadBlobInto workaround in serialize.go may no longer be necessary)")
	}
}
