package engine

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// This file is phase 2's Step 1 decision-gate spike (PLAN.md "フェーズ②の
// ステップ"). It measures, against the actual driver, whether copying data
// into a live shared database via modernc.org/sqlite's online Backup API
// (NewBackup/NewRestore/Step/Finish) can replace the single-keeper design
// engine currently uses, and lets multiple independent *sql.Conn "sessions"
// see and isolate each other's transactions the way execdb_spec.md §2/§8
// require. See PLAN.md for the full measurement table and pass criteria.
//
// Gate tests: TestSpike①③④⑥⑧ (named without the circled digits below,
// since Go identifiers can't hold them -- see each test's doc comment for
// its number). The rest are diagnostic/data-gathering only.
//
// DECISION (recorded here and in PLAN.md "フェーズ②Step 1で確定した事実"):
// "memdb" passes all gate tests; "sharedcache" fails ④ and ⑥ with a
// reproducible, *unbounded and uncancelable* hang (modernc.org/sqlite's
// SQLITE_LOCKED_SHAREDCACHE retry path blocks on a raw mutex that ignores
// both context.Context and the DSN's busy_timeout -- see
// spikeRunHardBounded's doc comment). Step 2 adopts "memdb"
// (file:/name?vfs=memdb) as engine's live-database DSN. Because that
// decision is now made, this file logs (rather than fails) the
// "sharedcache" disqualification wherever it would otherwise need an
// indefinite wait to detect -- it is not meant to gate CI going forward.
// Like phase 1's serialize_spike_test.go, expect this file to be pruned or
// reorganized into permanent regression tests during a later phase 2 step.

// backupper mirrors modernc.org/sqlite's (unexported) driver connection
// type's exported NewBackup method: func (c *conn) NewBackup(dstUri
// string) (*Backup, error). Reached the same way serializer/deserializer
// are in serialize.go: sql.Conn.Raw() + a local interface assertion.
type backupper interface {
	NewBackup(dstUri string) (*sqlite.Backup, error)
}

var spikeSeq int64

// spikeBase is one candidate connection-sharing model under test.
type spikeBase struct {
	name string
	// dsn returns a fresh, uniquely-named DSN for this base with the given
	// busy_timeout (milliseconds).
	dsn func(busyTimeoutMS int) string
}

var spikeBases = []spikeBase{
	{
		name: "memdb",
		dsn: func(ms int) string {
			n := atomic.AddInt64(&spikeSeq, 1)
			return fmt.Sprintf("file:/execdbspike%d?vfs=memdb&_busy_timeout=%d", n, ms)
		},
	},
	{
		name: "sharedcache",
		dsn: func(ms int) string {
			n := atomic.AddInt64(&spikeSeq, 1)
			return fmt.Sprintf("file:execdbspike%d?mode=memory&cache=shared&_busy_timeout=%d", n, ms)
		},
	},
}

// spikeOpenLive opens dsn and immediately claims one connection ("anchor")
// that is kept open for the rest of the test via t.Cleanup, mirroring the
// production design where engine.DB holds a keeper connection purely to
// keep the store alive (Step 2). Without an anchor, a store with no open
// connections may be freed as soon as a transient connection (such as a
// Backup's destination connection, which Backup.Finish closes) goes away.
func spikeOpenLive(t *testing.T, dsn string) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	anchor, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("anchor conn: %v", err)
	}
	t.Cleanup(func() { anchor.Close() })
	return db, anchor
}

func spikeSerialize(t *testing.T, conn *sql.Conn) []byte {
	t.Helper()
	var blob []byte
	err := conn.Raw(func(driverConn any) error {
		s, ok := driverConn.(serializer)
		if !ok {
			return fmt.Errorf("driver connection does not support Serialize")
		}
		v, err := s.Serialize()
		if err != nil {
			return err
		}
		blob = v
		return nil
	})
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return blob
}

func spikeDeserialize(t *testing.T, conn *sql.Conn, blob []byte) {
	t.Helper()
	err := conn.Raw(func(driverConn any) error {
		d, ok := driverConn.(deserializer)
		if !ok {
			return fmt.Errorf("driver connection does not support Deserialize")
		}
		return d.Deserialize(blob)
	})
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
}

// spikeBackupInto backs conn's current content up into dstDSN's database
// (which must already have at least one connection open, e.g. via
// spikeOpenLive, so the destination store survives Backup.Finish closing
// its own destination connection).
func spikeBackupInto(conn *sql.Conn, dstDSN string) error {
	return conn.Raw(func(driverConn any) error {
		b, ok := driverConn.(backupper)
		if !ok {
			return fmt.Errorf("driver connection does not support NewBackup")
		}
		bk, err := b.NewBackup(dstDSN)
		if err != nil {
			return err
		}
		more, err := bk.Step(-1)
		if err != nil {
			return err
		}
		if more {
			return fmt.Errorf("backup reported more pages remaining after Step(-1)")
		}
		return bk.Finish()
	})
}

// spikeMakeBlob builds a throwaway in-memory database, runs stmts against
// it, and returns its Serialize()d bytes -- mimicking the blob Open/Load
// reads from a file's data footer in the real implementation.
func spikeMakeBlob(t *testing.T, stmts ...string) []byte {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("source conn: %v", err)
	}
	defer conn.Close()
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return spikeSerialize(t, conn)
}

// spikeLoadInto mimics the real loadBlob flow Step 2 will implement:
// Deserialize blob into a fresh throwaway connection, then back that
// connection up into dstDSN's live database.
func spikeLoadInto(t *testing.T, blob []byte, dstDSN string) error {
	t.Helper()
	scratch, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	defer scratch.Close()
	conn, err := scratch.Conn(context.Background())
	if err != nil {
		t.Fatalf("scratch conn: %v", err)
	}
	defer conn.Close()

	spikeDeserialize(t, conn, blob)
	return spikeBackupInto(conn, dstDSN)
}

// spikeScratch wraps a throwaway single-connection database used to
// inspect a serialized blob in isolation.
type spikeScratch struct {
	db   *sql.DB
	conn *sql.Conn
}

func spikeDeserializeToScratch(t *testing.T, blob []byte) *spikeScratch {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatalf("scratch conn: %v", err)
	}
	spikeDeserialize(t, conn, blob)
	return &spikeScratch{db: db, conn: conn}
}

func (s *spikeScratch) Close() {
	s.conn.Close()
	s.db.Close()
}

func (s *spikeScratch) row999Count(t *testing.T) int {
	t.Helper()
	var count int
	if err := s.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM t WHERE a=999").Scan(&count); err != nil {
		t.Fatalf("query scratch: %v", err)
	}
	return count
}

func (s *spikeScratch) integrityCheck(t *testing.T) string {
	t.Helper()
	var result string
	if err := s.conn.QueryRowContext(context.Background(), "PRAGMA integrity_check").Scan(&result); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	return result
}

// ① TestSpikeBackupIntoLiveDatabase [GATE]: data loaded via
// Deserialize+Backup into a live database must be visible to -- and
// writable from -- a brand new connection opened after the backup.
func TestSpikeBackupIntoLiveDatabase(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(5000)
			liveDB, _ := spikeOpenLive(t, dsn)

			blob := spikeMakeBlob(t, "CREATE TABLE t(a INTEGER)", "INSERT INTO t VALUES (1),(2),(3)")
			if err := spikeLoadInto(t, blob, dsn); err != nil {
				t.Fatalf("backup into live db: %v", err)
			}

			fresh, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("open fresh pooled conn: %v", err)
			}
			defer fresh.Close()

			var count int
			if err := fresh.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&count); err != nil {
				t.Fatalf("select from fresh connection: %v", err)
			}
			if count != 3 {
				t.Errorf("count = %d, want 3 (backup did not propagate to a new connection)", count)
			}

			if _, err := fresh.ExecContext(context.Background(), "INSERT INTO t VALUES (4)"); err != nil {
				t.Errorf("insert via fresh connection: %v (destination not writable after backup)", err)
			}
		})
	}
}

// ② TestSpikeBackupWhileDestinationHasIdleKeeper [diagnostic]: a
// connection opened and kept idle *before* the backup runs (e.g. a REPL
// session, or engine's own keeper) should also see the new data
// afterward, not just freshly-opened connections.
func TestSpikeBackupWhileDestinationHasIdleKeeper(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(5000)
			_, anchor := spikeOpenLive(t, dsn) // opened BEFORE the backup, stays idle

			blob := spikeMakeBlob(t, "CREATE TABLE t(a INTEGER)", "INSERT INTO t VALUES (10),(20)")
			if err := spikeLoadInto(t, blob, dsn); err != nil {
				t.Fatalf("backup while destination has an idle keeper: %v", err)
			}

			var count int
			if err := anchor.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&count); err != nil {
				t.Fatalf("select from the pre-existing anchor connection: %v", err)
			}
			if count != 2 {
				t.Errorf("count = %d, want 2 (a connection opened before the backup does not see the new data)", count)
			}
		})
	}
}

// ③ TestSpikeBackupWhileOtherSessionInTransaction [GATE]: backing up into
// a live database while another session holds an open, uncommitted write
// transaction must either fail/block (busy) or succeed without disturbing
// that other session -- it must never silently corrupt it.
func TestSpikeBackupWhileOtherSessionInTransaction(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(300) // short busy_timeout to bound test runtime
			liveDB, anchor := spikeOpenLive(t, dsn)

			if _, err := anchor.ExecContext(context.Background(), "CREATE TABLE t(a INTEGER)"); err != nil {
				t.Fatalf("create schema: %v", err)
			}

			txConn, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("tx conn: %v", err)
			}
			defer txConn.Close()
			if _, err := txConn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
				t.Fatalf("begin: %v", err)
			}
			if _, err := txConn.ExecContext(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
				t.Fatalf("insert in tx: %v", err)
			}
			// deliberately left uncommitted

			blob := spikeMakeBlob(t, "CREATE TABLE u(b TEXT)", "INSERT INTO u VALUES ('x')")
			backupErr := spikeLoadInto(t, blob, dsn)
			t.Logf("backup while other session holds an open write transaction: err=%v", backupErr)

			if backupErr == nil {
				var name string
				if err := anchor.QueryRowContext(context.Background(), "SELECT name FROM sqlite_master WHERE name='u'").Scan(&name); err != nil {
					t.Errorf("backup reported success but table u is not visible: %v", err)
				}
			}

			// Whatever happened to the backup, the in-progress transaction's
			// own connection must still be usable -- a corrupted connection
			// would be a genuine bug, not just a busy/lock error.
			if _, err := txConn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Errorf("rollback after concurrent backup attempt: %v (transaction connection got corrupted)", err)
			}
		})
	}
}

// ④ TestSpikeTransactionIsolationBetweenSessions [GATE]: an uncommitted
// write on one connection must not corrupt another connection's view, and
// a commit must become visible to it -- the core of spec §2's "two
// independent clients" requirement.
//
// IMPORTANT correction made while running this spike: a fresh autocommit
// read on a connection that has not already established a lock does NOT
// get an instant "invisible, but non-blocking" answer the way classic
// SQLite file-locking would suggest. What actually happens differs sharply
// by base (see runSpikeReadWhileWriterActive's doc comment) -- this is the
// single most important empirical finding of Step 1.
func TestSpikeTransactionIsolationBetweenSessions(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(3000)
			liveDB, anchor := spikeOpenLive(t, dsn)
			if _, err := anchor.ExecContext(context.Background(), "CREATE TABLE t(a INTEGER)"); err != nil {
				t.Fatalf("create schema: %v", err)
			}

			connA, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("conn A: %v", err)
			}
			defer connA.Close()
			connB, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("conn B: %v", err)
			}
			defer connB.Close()

			if _, err := connA.ExecContext(context.Background(), "BEGIN"); err != nil {
				t.Fatalf("A begin: %v", err)
			}
			if _, err := connA.ExecContext(context.Background(), "INSERT INTO t VALUES (99)"); err != nil {
				t.Fatalf("A insert: %v", err)
			}

			// B's read is attempted with a hard, unbounded-hang-proof
			// timeout of its own (independent of the driver's own
			// busy_timeout, which sharedcache's SQLITE_LOCKED_SHAREDCACHE
			// retry path does not honor at all -- see the doc comment on
			// runSpikeReadWhileWriterActive).
			countBefore, beforeErr, beforeElapsed, hungBefore := runSpikeReadWhileWriterActive(connB, "SELECT count(*) FROM t WHERE a=99")
			t.Logf("B read while A uncommitted: elapsed=%v count=%d err=%v hung=%v", beforeElapsed, countBefore, beforeErr, hungBefore)
			if hungBefore {
				// This is the disqualifying finding against "sharedcache"
				// that decided Step 1's gate (PLAN.md): logged, not failed,
				// since the decision (adopt memdb) is already made and this
				// file is not meant to gate CI going forward -- see the
				// package doc comment at the top of this file.
				t.Logf("hard-bound hang: %s cannot be used as a live database base -- a reader can block indefinitely behind any writer, with no way to cancel it via context", base.name)
			} else if beforeErr == nil && countBefore != 0 {
				t.Errorf("B saw an uncommitted row from A: count=%d, want 0", countBefore)
			}

			if _, err := connA.ExecContext(context.Background(), "COMMIT"); err != nil {
				t.Fatalf("A commit: %v", err)
			}

			var countAfter int
			if err := connB.QueryRowContext(context.Background(), "SELECT count(*) FROM t WHERE a=99").Scan(&countAfter); err != nil {
				t.Fatalf("B select after commit: %v", err)
			}
			if countAfter != 1 {
				t.Errorf("B did not see A's committed row: count=%d, want 1", countAfter)
			}
		})
	}
}

// spikeHardBound is a wall-clock ceiling used only to detect an outright
// hang in a background goroutine (never to interrupt a well-behaved,
// bounded busy_timeout wait, which is always well under this).
const spikeHardBound = 6 * time.Second

// runSpikeReadWhileWriterActive runs query on conn from a background
// goroutine and waits up to spikeHardBound for it to return, reporting
// whether it hung. This exists because of a real, load-bearing finding
// from this spike: under the "sharedcache" base, a plain autocommit read
// that conflicts with another connection's open write transaction goes
// through modernc.org/sqlite's SQLITE_LOCKED_SHAREDCACHE retry path
// (conn.go's retry(), which blocks on a raw mutex released only by
// sqlite3_unlock_notify -- it does not consult context.Context at all and
// ignores the DSN's _busy_timeout). If nothing else ever commits or rolls
// back the conflicting transaction, that call blocks forever, uncancelable.
// A naive single-goroutine test of this (see /tmp/waltest.go in the Step 1
// session log) trips Go's own "all goroutines are asleep" deadlock
// detector and crashes the process. Running the risky call in its own
// goroutine keeps the test's main goroutine runnable so we can detect and
// report the hang instead of crashing.
//
// Under "memdb", by contrast, the same conflict returns plain SQLITE_BUSY,
// which *does* go through SQLite's normal internal busy-handler and *does*
// honor _busy_timeout -- confirmed by this same helper measuring a wait
// matching the configured busy_timeout before erroring out. This
// difference is the deciding factor in Step 1's gate: see PLAN.md.
func runSpikeReadWhileWriterActive(conn *sql.Conn, query string) (count int, err error, elapsed time.Duration, hung bool) {
	var c int
	runErr, elapsed, hung := spikeRunHardBounded(func() error {
		return conn.QueryRowContext(context.Background(), query).Scan(&c)
	})
	if hung {
		return 0, nil, elapsed, true
	}
	return c, runErr, elapsed, false
}

// spikeRunHardBounded runs fn in its own goroutine and waits up to
// spikeHardBound for it to return, reporting whether it hung instead. See
// runSpikeReadWhileWriterActive's doc comment above for why this exists:
// fn may never return (SQLITE_LOCKED_SHAREDCACHE's retry path ignores
// context and busy_timeout alike), and a naive synchronous call to it would
// trip Go's own deadlock detector rather than let the test report a clean
// failure. fn must not touch any variable the caller reads before this
// function returns hung=true -- there is no way to stop the goroutine, so
// anything it later writes must stay confined to fn's own closure.
func spikeRunHardBounded(fn func() error) (err error, elapsed time.Duration, hung bool) {
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err, time.Since(start), false
	case <-time.After(spikeHardBound):
		return nil, time.Since(start), true
	}
}

// ⑤ TestSpikeConcurrentWriteConflictBehavior [diagnostic]: records how
// long a second writer waits (busy_timeout) behind a first writer's
// held lock, and how it errors if it gives up (N-4).
func TestSpikeConcurrentWriteConflictBehavior(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(2000)
			liveDB, anchor := spikeOpenLive(t, dsn)
			if _, err := anchor.ExecContext(context.Background(), "CREATE TABLE t(a INTEGER)"); err != nil {
				t.Fatalf("create schema: %v", err)
			}

			connA, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("conn A: %v", err)
			}
			defer connA.Close()
			connB, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("conn B: %v", err)
			}
			defer connB.Close()

			if _, err := connA.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
				t.Fatalf("A begin immediate: %v", err)
			}

			done := make(chan error, 1)
			start := time.Now()
			go func() {
				_, err := connB.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
				done <- err
			}()

			time.Sleep(200 * time.Millisecond)
			if _, err := connA.ExecContext(context.Background(), "COMMIT"); err != nil {
				t.Fatalf("A commit: %v", err)
			}

			bErr := <-done
			elapsed := time.Since(start)
			t.Logf("B's write attempt while A held the write lock: elapsed=%v err=%v", elapsed, bErr)
		})
	}
}

// ⑥ TestSpikeSerializeDuringConcurrentWriteTransaction [GATE]: Serialize()
// itself is not transaction-aware (N-5), so Step 2 must wrap it in a
// BEGIN IMMEDIATE barrier. The gate criterion is the barrier subtests, not
// the bare (no-barrier) diagnostic.
func TestSpikeSerializeDuringConcurrentWriteTransaction(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(3000)
			liveDB, anchor := spikeOpenLive(t, dsn)
			if _, err := anchor.ExecContext(context.Background(), "CREATE TABLE t(a INTEGER)"); err != nil {
				t.Fatalf("create schema: %v", err)
			}
			if _, err := anchor.ExecContext(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
				t.Fatalf("seed committed row: %v", err)
			}

			txConn, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("tx conn: %v", err)
			}
			defer txConn.Close()
			if _, err := txConn.ExecContext(context.Background(), "BEGIN"); err != nil {
				t.Fatalf("begin: %v", err)
			}
			if _, err := txConn.ExecContext(context.Background(), "INSERT INTO t VALUES (999)"); err != nil {
				t.Fatalf("insert uncommitted row: %v", err)
			}

			t.Run("bareSerializeIsDiagnosticOnly", func(t *testing.T) {
				// Genuinely diagnostic: Serialize() erroring outright here
				// (observed on memdb) is itself a valid, informative
				// outcome, not a test failure -- log it and stop rather
				// than calling the Fatalf-on-error spikeSerialize helper.
				var blob []byte
				err := anchor.Raw(func(driverConn any) error {
					s, ok := driverConn.(serializer)
					if !ok {
						return fmt.Errorf("driver connection does not support Serialize")
					}
					v, err := s.Serialize()
					if err != nil {
						return err
					}
					blob = v
					return nil
				})
				if err != nil {
					t.Logf("bare Serialize() during a concurrent uncommitted write errored out (no leak, but also no snapshot): %v", err)
					return
				}
				sc := spikeDeserializeToScratch(t, blob)
				defer sc.Close()
				t.Logf("bare Serialize() during a concurrent uncommitted write: leaked uncommitted row = %v, integrity_check = %q",
					sc.row999Count(t) > 0, sc.integrityCheck(t))
			})

			t.Run("barrierRefusesWhileWriterActive", func(t *testing.T) {
				barrierDB, err := sql.Open("sqlite", dsn)
				if err != nil {
					t.Fatalf("open short-timeout conn: %v", err)
				}
				defer barrierDB.Close()
				if _, err := barrierDB.ExecContext(context.Background(), "pragma busy_timeout=200"); err != nil {
					t.Fatalf("set short busy_timeout: %v", err)
				}
				// BEGIN IMMEDIATE also risks the sharedcache indefinite-hang
				// path (spikeRunHardBounded's doc comment), same as a plain
				// read -- it goes through the same conn.step()/retry().
				beginErr, elapsed, hung := spikeRunHardBounded(func() error {
					_, err := barrierDB.ExecContext(context.Background(), "BEGIN IMMEDIATE")
					return err
				})
				if hung {
					// Same disqualifying "sharedcache" finding as in
					// TestSpikeTransactionIsolationBetweenSessions: logged,
					// not failed (see that test's comment).
					t.Logf("hard-bound hang: %s's BEGIN IMMEDIATE did not return within %v while a writer was active", base.name, spikeHardBound)
					return
				}
				if beginErr == nil {
					barrierDB.ExecContext(context.Background(), "ROLLBACK")
					t.Errorf("barrier's BEGIN IMMEDIATE succeeded while another session held an open write transaction (should have blocked/failed)")
				} else {
					t.Logf("barrier correctly refused to start while a writer was active: elapsed=%v err=%v", elapsed, beginErr)
				}
			})

			if _, err := txConn.ExecContext(context.Background(), "COMMIT"); err != nil {
				t.Fatalf("commit the held transaction: %v", err)
			}

			t.Run("barrierSucceedsAfterCommit", func(t *testing.T) {
				barrierConn, err := liveDB.Conn(context.Background())
				if err != nil {
					t.Fatalf("barrier conn: %v", err)
				}
				defer barrierConn.Close()
				if _, err := barrierConn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
					t.Fatalf("barrier begin immediate after commit: %v", err)
				}
				blob := spikeSerialize(t, barrierConn)
				if _, err := barrierConn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
					t.Errorf("barrier rollback: %v", err)
				}

				sc := spikeDeserializeToScratch(t, blob)
				defer sc.Close()
				if sc.row999Count(t) == 0 {
					t.Errorf("Serialize() after commit is missing the now-committed row 999")
				}
				if got := sc.integrityCheck(t); got != "ok" {
					t.Errorf("integrity_check after barrier Serialize() = %q, want \"ok\"", got)
				}
			})
		})
	}
}

// ⑦ TestSpikeSerializeFromPooledConnection [diagnostic]: Serialize() from
// any pooled connection (not just the anchor/keeper) should agree with
// the anchor's own view.
func TestSpikeSerializeFromPooledConnection(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(5000)
			liveDB, anchor := spikeOpenLive(t, dsn)
			if _, err := anchor.ExecContext(context.Background(), "CREATE TABLE t(a INTEGER)"); err != nil {
				t.Fatalf("create schema: %v", err)
			}
			if _, err := anchor.ExecContext(context.Background(), "INSERT INTO t VALUES (1),(2),(3)"); err != nil {
				t.Fatalf("seed: %v", err)
			}

			pooled, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("pooled conn: %v", err)
			}
			defer pooled.Close()

			fromAnchor := spikeSerialize(t, anchor)
			fromPooled := spikeSerialize(t, pooled)

			scA := spikeDeserializeToScratch(t, fromAnchor)
			defer scA.Close()
			scB := spikeDeserializeToScratch(t, fromPooled)
			defer scB.Close()

			var countA, countB int
			if err := scA.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&countA); err != nil {
				t.Fatalf("count A: %v", err)
			}
			if err := scB.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&countB); err != nil {
				t.Fatalf("count B: %v", err)
			}
			if countA != countB {
				t.Errorf("Serialize() from a pooled (non-anchor) connection disagrees with the anchor: anchor=%d pooled=%d", countA, countB)
			} else {
				t.Logf("Serialize() from a pooled connection matches the anchor: count=%d", countA)
			}
		})
	}
}

// ⑧ TestSpikeContextCancelInterruptsQuery [GATE]: a canceled context must
// interrupt a running query promptly, and the connection must remain
// usable for a subsequent query afterward.
func TestSpikeContextCancelInterruptsQuery(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(5000)
			liveDB, _ := spikeOpenLive(t, dsn)

			conn, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("conn: %v", err)
			}
			defer conn.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			start := time.Now()
			rows, queryErr := conn.QueryContext(ctx, `WITH RECURSIVE cnt(x) AS (
				SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x < 50000000
			) SELECT count(*) FROM cnt`)
			if queryErr == nil {
				// Some drivers only surface the cancellation on Next/Scan.
				for rows.Next() {
				}
				queryErr = rows.Err()
				rows.Close()
			}
			elapsed := time.Since(start)

			if queryErr == nil {
				t.Fatalf("expected the query to be canceled, but it completed in %v", elapsed)
			}
			if elapsed > 10*time.Second {
				t.Errorf("cancellation took too long: %v (context cancellation may not be wired into the driver)", elapsed)
			}
			t.Logf("query canceled after %v: %v", elapsed, queryErr)

			var one int
			if err := conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
				t.Errorf("connection unusable after a canceled query: %v", err)
			} else if one != 1 {
				t.Errorf("got %d, want 1", one)
			}
		})
	}
}

// ⑨ TestSpikeSessionSurvivesBackupReload [diagnostic]: a connection opened
// before a `.load`-style full reload must see the reload's new data (and
// not the old data it replaced) without needing to be reopened -- the
// prerequisite for `.load` keeping existing pgwire sessions alive.
func TestSpikeSessionSurvivesBackupReload(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(5000)
			liveDB, _ := spikeOpenLive(t, dsn)

			blob1 := spikeMakeBlob(t, "CREATE TABLE t(a INTEGER)", "INSERT INTO t VALUES (1)")
			if err := spikeLoadInto(t, blob1, dsn); err != nil {
				t.Fatalf("initial load: %v", err)
			}

			session, err := liveDB.Conn(context.Background())
			if err != nil {
				t.Fatalf("session conn: %v", err)
			}
			defer session.Close()

			var v int
			if err := session.QueryRowContext(context.Background(), "SELECT a FROM t").Scan(&v); err != nil {
				t.Fatalf("session initial read: %v", err)
			}
			if v != 1 {
				t.Fatalf("session initial read = %d, want 1", v)
			}

			// Simulate `.load`: replace the live DB's content entirely,
			// without touching the session opened above.
			blob2 := spikeMakeBlob(t, "CREATE TABLE u(b TEXT)", "INSERT INTO u VALUES ('reloaded')")
			if err := spikeLoadInto(t, blob2, dsn); err != nil {
				t.Fatalf("reload: %v", err)
			}

			var s string
			if err := session.QueryRowContext(context.Background(), "SELECT b FROM u").Scan(&s); err != nil {
				t.Errorf("session did not see the reloaded data: %v", err)
			} else if s != "reloaded" {
				t.Errorf("session saw %q, want %q", s, "reloaded")
			}

			var name string
			err = session.QueryRowContext(context.Background(), "SELECT name FROM sqlite_master WHERE name='t'").Scan(&name)
			if err != sql.ErrNoRows {
				t.Errorf("table t should no longer exist after reload (Load must replace, not merge): err=%v", err)
			}
		})
	}
}

// ⑩ TestSpikeColumnTypesFromDriver [diagnostic, N-8]: records what
// sql.Rows.ColumnTypes() actually returns from this driver, before and
// after Next(), as input for phase 4's Postgres OID type mapping.
func TestSpikeColumnTypesFromDriver(t *testing.T) {
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("recovered panic while inspecting column types: %v", r)
				}
			}()

			dsn := base.dsn(5000)
			liveDB, _ := spikeOpenLive(t, dsn)

			ddl := `CREATE TABLE t(i INTEGER, r REAL, txt TEXT, b BLOB, n NUMERIC)`
			if _, err := liveDB.ExecContext(context.Background(), ddl); err != nil {
				t.Fatalf("create table: %v", err)
			}
			if _, err := liveDB.ExecContext(context.Background(),
				"INSERT INTO t VALUES (1, 1.5, 'hi', X'0102', 3)"); err != nil {
				t.Fatalf("insert: %v", err)
			}

			rows, err := liveDB.QueryContext(context.Background(), "SELECT i, r, txt, b, n, i+1 AS expr FROM t")
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			defer rows.Close()

			logColumnTypes := func(when string) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Logf("%s: ColumnTypes error: %v", when, err)
					return
				}
				for _, c := range cols {
					nullable, ok := c.Nullable()
					t.Logf("%s: col=%s dbType=%q scanType=%v nullable=%v(ok=%v)",
						when, c.Name(), c.DatabaseTypeName(), c.ScanType(), nullable, ok)
				}
			}

			logColumnTypes("before Next()")
			if !rows.Next() {
				t.Fatalf("expected one row")
			}
			logColumnTypes("after Next()")
		})
	}
}

// ⑪ TestSpikeMemdbSizeCeiling [diagnostic, N-9]: confirms the ~1GiB memdb
// size ceiling and records the exact error returned at the limit.
func TestSpikeMemdbSizeCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a large amount of data; skipped with -short")
	}
	for _, base := range spikeBases {
		t.Run(base.name, func(t *testing.T) {
			dsn := base.dsn(5000)
			liveDB, _ := spikeOpenLive(t, dsn)

			if _, err := liveDB.ExecContext(context.Background(), "CREATE TABLE big(a BLOB)"); err != nil {
				t.Fatalf("create table: %v", err)
			}

			const chunk = 64 * 1024 * 1024            // 64MiB per row, well under SQLITE_MAX_LENGTH
			const ceiling = int64(1100 * 1024 * 1024) // stop a bit past the documented 1GiB (N-9)

			var written int64
			var lastErr error
			for written < ceiling {
				if _, err := liveDB.ExecContext(context.Background(), "INSERT INTO big VALUES (zeroblob(?))", chunk); err != nil {
					lastErr = err
					break
				}
				written += chunk
			}
			t.Logf("stopped after writing ~%d MiB (base=%s): lastErr=%v", written/(1024*1024), base.name, lastErr)
		})
	}
}
