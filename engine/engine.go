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

// dbSeq gives each in-memory database a unique name in the shared-cache
// namespace (spec §7: "file:<name>?mode=memory&cache=shared"), so
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
	keeper *sql.Conn // holds the shared-cache database alive (spec §7)

	sourcePath string // "" if this DB was not opened from a file (New())
	engineSize int64  // bytes to carry forward as Snapshot's engine prefix
	info       Info
}

// newSharedCacheDB opens a fresh shared-cache in-memory database and
// returns a keeper connection that must stay open for the database's
// entire lifetime, or the last connection to close would free it.
func newSharedCacheDB() (*sql.DB, *sql.Conn, error) {
	name := fmt.Sprintf("execdb%d", atomic.AddInt64(&dbSeq, 1))
	dsn := "file:" + name + "?mode=memory&cache=shared"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	keeper, err := sdb.Conn(context.Background())
	if err != nil {
		sdb.Close()
		return nil, nil, err
	}
	return sdb, keeper, nil
}

// New creates an empty in-memory database with no backing file.
func New() (*DB, error) {
	sdb, keeper, err := newSharedCacheDB()
	if err != nil {
		return nil, err
	}
	return &DB{sdb: sdb, keeper: keeper}, nil
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
		if err := deserializeInto(db.keeper, blob); err != nil {
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
// Exec, Query and QueryRow all run on the keeper connection rather than a
// pooled connection from db.sdb. modernc.org/sqlite's Deserialize (spec
// §7) only affects the connection it is called on -- even a connection
// opened afterward on the same shared-cache DSN does not see its result
// (confirmed in engine/serialize_test.go) -- so every read of a DB that
// may have been populated via Open/OpenSelf/Load must go through the same
// connection that Deserialize ran on.
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.keeper.ExecContext(context.Background(), query, args...)
}

func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.keeper.QueryContext(context.Background(), query, args...)
}

func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.keeper.QueryRowContext(context.Background(), query, args...)
}

// Close releases the database's resources. It does not persist anything;
// callers that want to keep the data must call Snapshot or Overwrite
// first (spec §4: persistence is explicit only).
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.keeper != nil {
		db.keeper.Close()
	}
	return db.sdb.Close()
}
