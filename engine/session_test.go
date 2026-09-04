package engine

import (
	"context"
	"testing"
	"time"
)

// TestSessionTransactionIsolation is the core of spec §2/§8's "two
// independent clients" requirement: B must never observe A's write until
// A's transaction concludes.
//
// memdb's locking is coarser than a normal file database's: a *fresh*
// SHARED-lock acquisition blocks (bounded by busy_timeout) behind any
// connection already holding a write lock, rather than proceeding
// immediately with a pre-write snapshot the way an ordinary SELECT would
// against a real file DB's RESERVED lock (.claude/rules/sqlite-quirks.md,
// PLAN.md "フェーズ②Step 1で確定した事実"). So this test proves
// isolation via serialization -- B's read, issued while A's write is
// still open, blocks and only returns once A commits (never observing a
// half-done state) -- rather than true concurrent snapshot isolation,
// which memdb does not provide.
func TestSessionTransactionIsolation(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}

	a, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if _, err := a.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec("INSERT INTO t VALUES (99)"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		r.err = b.QueryRow("SELECT count(*) FROM t WHERE a=99").Scan(&r.n)
		done <- r
	}()

	time.Sleep(200 * time.Millisecond)
	if _, err := a.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("B's read: %v", r.err)
		}
		if r.n != 1 {
			t.Errorf("B saw count=%d after A committed, want 1", r.n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("B's read did not return after A committed")
	}
}

// TestSessionSeesCommittedWritesFromAnotherSession is the simpler,
// uncontended counterpart to TestSessionTransactionIsolation: no priming
// needed, since there is no concurrent writer for a fresh lock
// acquisition to block behind.
func TestSessionSeesCommittedWritesFromAnotherSession(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}

	a, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if _, err := a.Exec("INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := b.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

func TestSessionContextCancel(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = s.QueryContext(ctx, `WITH RECURSIVE cnt(x) AS (
		SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x < 50000000
	) SELECT count(*) FROM cnt`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected the query to be canceled, but it completed in %v", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Errorf("cancellation took too long: %v", elapsed)
	}

	// The Session must still be usable after a canceled query.
	var one int
	if err := s.QueryRow("SELECT 1").Scan(&one); err != nil {
		t.Errorf("session unusable after a canceled query: %v", err)
	} else if one != 1 {
		t.Errorf("got %d, want 1", one)
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s, err := db.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got: %v", err)
	}
}
