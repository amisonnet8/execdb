package engine

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewIsEmpty(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT name FROM sqlite_master")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("expected no schema objects in a fresh New() database")
	}

	if info := db.Info(); info.HasData {
		t.Error("expected Info().HasData=false for New()")
	}
}

func TestOpenNonexistentIsEmpty(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Open of a nonexistent path should not error, got: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatalf("expected a fresh empty DB to accept writes: %v", err)
	}
}

func TestSharedCacheDSNIsVisibleToOtherConnections(t *testing.T) {
	// This is what makes it possible, in later phases, for the REPL and
	// the external I/F to act as two independent clients against the same
	// in-memory database (spec §2/§8): plain SQL written through the
	// keeper connection is visible to a separate *sql.Conn opened
	// afterward against the same shared-cache DSN. Note the limit of this:
	// modernc.org/sqlite's Deserialize does NOT propagate this way (see
	// engine/serialize_test.go), which is why DB.Exec/Query/QueryRow all
	// route through the keeper connection rather than db.sdb's pool.
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES (7)"); err != nil {
		t.Fatal(err)
	}

	other, err := db.sdb.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	var n int
	if err := other.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("a separately opened connection could not see the keeper's writes: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}
