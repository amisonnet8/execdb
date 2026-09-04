package engine

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

// defaultBusyTimeoutMS bounds how long any connection to the live
// database waits behind another connection's lock before giving up with
// SQLITE_BUSY (spec §2: rely on SQLite's own concurrency machinery rather
// than reimplementing it). See .claude/rules/sqlite-quirks.md for why
// this matters specifically for the memdb VFS.
const defaultBusyTimeoutMS = 5000

// dbSeq gives each in-memory database a unique name in the memdb VFS's
// shared-store namespace (a name starting with "/" is shared globally by
// name within the process -- see .claude/rules/sqlite-quirks.md), so
// multiple DBs in the same process (e.g. in tests) never collide.
var dbSeq int64

// DB is an in-memory SQL database backed by modernc.org/sqlite, whose
// state can be persisted as a new executable (Snapshot) or by overwriting
// the host process's own executable in place (Overwrite). See
// execdb_spec.md §6 for the full design. The zero value is not usable;
// construct a DB with New, Open, or OpenSelf.
type DB struct {
	mu     sync.RWMutex
	sdb    *sql.DB
	keeper *sql.Conn // keeps the live memdb store alive; never used to run SQL (see loadBlobInto)
	dsn    string    // this DB's live memdb DSN -- the backup destination for Open/Load
	closed bool

	sourcePath string // "" if this DB was not opened from a file (New())
	engineSize int64  // bytes to carry forward as Snapshot's engine prefix
	info       Info
}

// newLiveDB opens a fresh memdb-backed live database and returns a
// keeper connection that must stay open for the database's entire
// lifetime (a store with no connections left open can be freed), plus
// the DSN callers need to reach the same store -- e.g. as a Backup
// destination (backup.go) or from DB.sdb's own connection pool.
//
// engine uses SQLite's memdb VFS (SHARED/RESERVED/EXCLUSIVE locking, the
// same family a normal file-backed database uses) rather than the
// mode=memory&cache=shared DSN phase 1 used: shared-cache's lock-conflict
// error (SQLITE_LOCKED_SHAREDCACHE) is retried via a wait that ignores
// both context.Context and busy_timeout and can hang forever. memdb's
// conflicts go through SQLite's normal busy-handler and honor
// busy_timeout, which bounds every wait (.claude/rules/sqlite-quirks.md,
// PLAN.md "フェーズ②Step 1で確定した事実").
func newLiveDB() (sdb *sql.DB, keeper *sql.Conn, dsn string, err error) {
	name := fmt.Sprintf("execdb%d", atomic.AddInt64(&dbSeq, 1))
	dsn = fmt.Sprintf("file:/%s?vfs=memdb&_busy_timeout=%d", name, defaultBusyTimeoutMS)
	sdb, err = sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, "", err
	}
	keeper, err = sdb.Conn(context.Background())
	if err != nil {
		sdb.Close()
		return nil, nil, "", err
	}
	return sdb, keeper, dsn, nil
}

// New creates an empty in-memory database with no backing file.
func New() (*DB, error) {
	sdb, keeper, dsn, err := newLiveDB()
	if err != nil {
		return nil, err
	}
	return &DB{sdb: sdb, keeper: keeper, dsn: dsn}, nil
}

// Open loads the data embedded in path, which may be either a plain
// ExecDB data file or an ExecDB-formatted executable (spec §6). A path
// that does not exist, or one with no ExecDB footer, yields an empty
// database; the bytes preceding any footer are remembered so a later
// Snapshot can carry them forward as the new file's engine prefix
// (spec §7).
func Open(path string) (*DB, error) {
	db, err := New()
	if err != nil {
		return nil, err
	}

	info, engineSize, blob, err := loadFromFile(path)
	if err != nil {
		db.Close()
		return nil, err
	}
	db.sourcePath = path
	db.engineSize = engineSize
	db.info = info

	if blob != nil {
		if err := loadBlobInto(blob, db.dsn); err != nil {
			db.Close()
			return nil, fmt.Errorf("engine: %s: %w", path, err)
		}
	}
	return db, nil
}

// OpenSelf loads the data embedded in the running process's own
// executable (os.Executable()). It also removes a leftover
// ".execdb_old" sidecar left behind by a previous Overwrite, on a
// best-effort basis (spec §7).
func OpenSelf() (*DB, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("engine: os.Executable: %w", err)
	}
	cleanupOrphanedOldSelf(self)
	return Open(self)
}

// loadFromFile inspects path and, if it holds an ExecDB footer, reads the
// data blob. A missing file or one with no footer is not an error -- both
// simply mean "no data yet", with engineSize set to whatever byte count a
// later Snapshot should carry forward as the engine prefix (spec §7).
func loadFromFile(path string) (info Info, engineSize int64, blob []byte, err error) {
	stat, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return Info{Path: path}, 0, nil, nil
		}
		return Info{}, 0, nil, statErr
	}

	info, err = Inspect(path)
	if err != nil {
		return Info{}, 0, nil, err
	}
	if !info.HasData {
		return info, stat.Size(), nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return Info{}, 0, nil, err
	}
	defer f.Close()

	blob = make([]byte, info.DataLength)
	if _, err := f.ReadAt(blob, info.DataOffset); err != nil {
		return Info{}, 0, nil, err
	}
	return info, info.DataOffset, blob, nil
}

// Info reports where this DB was loaded from and what its footer said.
func (db *DB) Info() Info {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.info
}

// Exec runs a DDL/DML/TCL statement. There is no restriction here: a Go
// caller using engine as a library is trusted (spec §6); access control
// by caller (REPL vs. external I/F) is cmd/execdb's responsibility.
//
// Exec, Query and QueryRow (and their *Context variants) each run on a
// connection borrowed from the pool (db.sdb), used once and returned --
// they are one-shot operations, not a session. A statement like BEGIN
// executed this way appears to succeed, but the transaction it opens is
// invisible to every later call: database/sql's ResetSession does not
// roll back an open transaction before returning a connection to the
// pool, so the next unrelated caller could silently inherit it
// (.claude/rules/sqlite-quirks.md). Callers that need
// BEGIN/COMMIT/ROLLBACK to mean anything must hold a single dedicated
// connection across those statements: use Session instead.
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.ExecContext(context.Background(), query, args...)
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	sdb, err := db.pooled()
	if err != nil {
		return nil, err
	}
	return sdb.ExecContext(ctx, query, args...)
}

func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.QueryContext(context.Background(), query, args...)
}

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	sdb, err := db.pooled()
	if err != nil {
		return nil, err
	}
	return sdb.QueryContext(ctx, query, args...)
}

func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.QueryRowContext(context.Background(), query, args...)
}

// QueryRowContext behaves like ExecContext/QueryContext, with one
// difference after Close: it cannot return ErrClosed directly, because
// *sql.Row has no way to carry a caller-supplied error before Scan is
// called. A QueryRowContext call made after Close instead surfaces
// database/sql's own "sql: database is closed" once Scan is called on
// the result.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.mu.RLock()
	sdb := db.sdb
	db.mu.RUnlock()
	return sdb.QueryRowContext(ctx, query, args...)
}

// Session opens a dedicated connection to db's live database: one
// independent client (spec §2/§8), such as a single pgwire connection or
// the REPL's own connection. Unlike Exec/Query/QueryRow, which borrow a
// connection from the pool for a single statement, a Session holds the
// same underlying connection across calls, so BEGIN/COMMIT/ROLLBACK
// executed through it behave like a real SQL transaction. The caller
// must Close it when done.
func (db *DB) Session(ctx context.Context) (*Session, error) {
	sdb, err := db.pooled()
	if err != nil {
		return nil, err
	}
	conn, err := sdb.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &Session{conn: conn}, nil
}

// pooled returns db.sdb for a one-shot call, or ErrClosed if db has
// already been closed.
func (db *DB) pooled() (*sql.DB, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, ErrClosed
	}
	return db.sdb, nil
}

// Close releases the database's resources. It does not persist anything;
// callers that want to keep the data must call Snapshot or Overwrite
// first (spec §4: persistence is explicit only). Close is idempotent: a
// second call is a no-op that returns nil.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	if db.keeper != nil {
		db.keeper.Close()
	}
	return db.sdb.Close()
}
