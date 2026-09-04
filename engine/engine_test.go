package engine

import (
	"context"
	"errors"
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

func TestPooledConnectionsShareTheLiveDatabase(t *testing.T) {
	// This is what makes it possible, in later phases, for the REPL and
	// the external I/F to act as two independent clients against the same
	// in-memory database (spec §2/§8): plain SQL written through the
	// keeper connection is visible to a separate *sql.Conn opened
	// afterward against the same live memdb DSN. Note the limit of this:
	// modernc.org/sqlite's Deserialize does NOT propagate this way (see
	// engine/serialize_test.go), which is why Open/Load go through
	// loadBlobInto's Deserialize-then-Backup dance instead of deserializing
	// straight into the live database.
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

// TestOpenIsVisibleToPooledConnections locks in the point of Step 2's
// redesign: Open's data must be visible through the ordinary connection
// pool (db.sdb), not just through some special internal connection. The
// old keeper-only design failed exactly this (Deserialize only affects
// the connection it ran on -- see serialize_test.go).
func TestOpenIsVisibleToPooledConnections(t *testing.T) {
	seed, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	if _, err := seed.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec("INSERT INTO t VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seed.execdb")
	if err := seed.Snapshot(path); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	conn, err := db.sdb.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var n int
	if err := conn.QueryRowContext(context.Background(), "SELECT a FROM t").Scan(&n); err != nil {
		t.Fatalf("a pooled connection could not see Open's data: %v", err)
	}
	if n != 42 {
		t.Errorf("got %d, want 42", n)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got: %v", err)
	}
}

func TestUseAfterCloseReturnsErrClosed(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); !errors.Is(err, ErrClosed) {
		t.Errorf("Exec after Close: got %v, want ErrClosed", err)
	}
	if _, err := db.Query("SELECT 1"); !errors.Is(err, ErrClosed) {
		t.Errorf("Query after Close: got %v, want ErrClosed", err)
	}
}

func TestExecContextRespectsCanceledContext(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before the call

	if _, err := db.ExecContext(ctx, "CREATE TABLE t(a INTEGER)"); err == nil {
		t.Error("expected ExecContext to fail against an already-canceled context")
	}
	if _, err := db.QueryContext(ctx, "SELECT 1"); err == nil {
		t.Error("expected QueryContext to fail against an already-canceled context")
	}
}
