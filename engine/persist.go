package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Snapshot writes the current state to path: the engine bytes this DB was
// opened with (if any) followed by a fresh data blob and footer (spec §4,
// REPL ".snapshot", CLI "--snapshot-as"). The write is atomic (a temporary
// file in the same directory, then renamed into place). The output is
// executable (0755) if this DB was opened from an executable, or a plain
// data file (0644) otherwise (spec §6).
//
// Snapshot does not generate a file name (timestamps, ".exe" completion):
// that is cmd/execdb's responsibility (naming.md); path is used as-is.
func (db *DB) Snapshot(path string) error {
	engineBytes, blob, err := db.image()
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if len(engineBytes) > 0 {
		mode = 0o755
	}
	return writeImageAtomic(path, engineBytes, blob, mode)
}

// SnapshotTo writes the same image Snapshot would to an arbitrary writer
// (spec §6: "汎用的な読み書き（任意のファイル、io.Writer）").
func (db *DB) SnapshotTo(w io.Writer) error {
	engineBytes, blob, err := db.image()
	if err != nil {
		return err
	}
	return writeImage(w, engineBytes, blob)
}

// image serializes the current DB state (through the serialization
// barrier, so a concurrent writer elsewhere cannot produce an
// inconsistent snapshot) and re-reads this DB's engine prefix from its
// source file, if any.
func (db *DB) image() (engineBytes, data []byte, err error) {
	db.mu.RLock()
	sourcePath := db.sourcePath
	engineSize := db.engineSize
	db.mu.RUnlock()

	data, err = db.serializeBarrier()
	if err != nil {
		return nil, nil, err
	}
	engineBytes, err = readEnginePrefix(sourcePath, engineSize)
	if err != nil {
		return nil, nil, err
	}
	return engineBytes, data, nil
}

// serializeBarrier takes a consistent snapshot of the live database.
// Serialize() itself is not transaction-aware -- called bare, it can
// return a torn snapshot that includes another session's uncommitted
// write (.claude/rules/sqlite-quirks.md) -- so this wraps it in a BEGIN
// IMMEDIATE barrier on a dedicated connection. Starting that transaction
// blocks (up to the live database's busy_timeout) until no other session
// holds a conflicting write lock, so once it succeeds, Serialize() cannot
// observe a write in progress. The barrier transaction itself never
// writes anything; it only exists to hold the write lock, so it always
// ends in ROLLBACK.
func (db *DB) serializeBarrier() ([]byte, error) {
	sdb, err := db.pooled()
	if err != nil {
		return nil, err
	}

	conn, err := sdb.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBusy, err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")

	blob, err := serializeConn(conn)
	if err != nil {
		return nil, err
	}
	if len(blob) == 0 {
		// modernc.org/sqlite's Serialize can return (nil, nil) rather than
		// an error when malloc fails for a very large database
		// (.claude/rules/sqlite-quirks.md) -- treat an empty result as a
		// failure rather than silently writing a zero-length data blob.
		return nil, ErrTooLarge
	}
	return blob, nil
}

// readEnginePrefix re-reads the first engineSize bytes of sourcePath
// immediately before a write, rather than keeping them resident: an
// engine-plus-modernc.org/sqlite binary runs 10-15MB, and a DB stays open
// far longer than a single Snapshot/Overwrite call (spec §7).
func readEnginePrefix(sourcePath string, engineSize int64) ([]byte, error) {
	if engineSize == 0 || sourcePath == "" {
		return nil, nil
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, engineSize)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeImage(w io.Writer, engineBytes, data []byte) error {
	if _, err := w.Write(engineBytes); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err := w.Write(encodeFooter(int64(len(engineBytes)), int64(len(data))))
	return err
}

// writeImageAtomic writes engineBytes+data+footer to path via a temporary
// file in the same directory followed by a rename, so a reader never
// observes a half-written file.
func writeImageAtomic(path string, engineBytes, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".execdb_tmp_*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if err := writeImage(tmp, engineBytes, data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// oldSuffix is the suffix used for the sidecar a running executable is
// renamed to during Overwrite, freeing up the original path for a fresh
// write (spec §7).
const oldSuffix = ".execdb_old"

// Overwrite replaces the running process's own executable (os.Executable())
// in place with its own engine bytes followed by the current DB state
// (spec §6, §7; REPL ".overwrite"). It always targets the current
// process's executable, regardless of which file this DB was opened from
// (spec §6: "ホストアプリ全体がそのままエンジン部分として扱われる"). The
// process itself is not terminated.
func (db *DB) Overwrite() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("engine: os.Executable: %w", err)
	}
	if looksLikeGoRunTempBinary(self) {
		return ErrNotOverwritable
	}

	selfInfo, err := Inspect(self)
	if err != nil {
		return err
	}
	engineSize := selfInfo.DataOffset
	if !selfInfo.HasData {
		stat, err := os.Stat(self)
		if err != nil {
			return err
		}
		engineSize = stat.Size()
	}

	data, err := db.serializeBarrier()
	if err != nil {
		return err
	}
	engineBytes, err := readEnginePrefix(self, engineSize)
	if err != nil {
		return err
	}

	return overwriteSelf(self, engineBytes, data)
}

// looksLikeGoRunTempBinary reports whether path sits under a `go-build*`
// directory inside the OS temp dir, the pattern `go run` uses for the
// binary it builds and deletes on exit.
func looksLikeGoRunTempBinary(path string) bool {
	tmp := os.TempDir()
	if tmp == "" {
		return false
	}
	rel, err := filepath.Rel(tmp, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && strings.Contains(rel, "go-build")
}

// overwriteSelf replaces the file at selfPath with engineBytes+data+footer
// even though selfPath is the currently running executable. A direct
// overwrite is rejected by the OS (Linux ETXTBSY, Windows
// ERROR_SHARING_VIOLATION), so instead:
//  1. rename selfPath to selfPath+oldSuffix (renaming a running executable
//     is allowed on both Linux and Windows)
//  2. write the new content to the now-vacated selfPath (a fresh file, not
//     an overwrite, so it succeeds on both OSes)
//  3. best-effort remove the sidecar (succeeds immediately on Linux;
//     Windows keeps it locked while this process runs, so it is cleaned
//     up on the next OpenSelf instead)
func overwriteSelf(selfPath string, engineBytes, data []byte) error {
	oldPath := selfPath + oldSuffix
	os.Remove(oldPath) // clear a leftover from an earlier run, if any

	if err := os.Rename(selfPath, oldPath); err != nil {
		return fmt.Errorf("engine: could not move aside the running executable: %w", err)
	}

	blob := make([]byte, 0, len(engineBytes)+len(data)+FooterSize)
	blob = append(blob, engineBytes...)
	blob = append(blob, data...)
	blob = append(blob, encodeFooter(int64(len(engineBytes)), int64(len(data)))...)

	if err := os.WriteFile(selfPath, blob, 0o755); err != nil {
		os.Rename(oldPath, selfPath) // best-effort restore
		return fmt.Errorf("engine: could not write the new executable: %w", err)
	}

	_ = os.Remove(oldPath)
	return nil
}

// cleanupOrphanedOldSelf best-effort removes a ".execdb_old" sidecar left
// behind by a previous Overwrite (only reachable on Windows, where the
// sidecar stays locked for as long as that earlier process kept running).
func cleanupOrphanedOldSelf(selfPath string) {
	os.Remove(selfPath + oldSuffix)
}

// Load replaces the in-memory state with the data embedded in path,
// discarding whatever this DB previously held; it does not merge
// (spec §4, REPL ".load"). Load writes no file, and it never references
// path's engine portion -- only its data -- even when path is itself an
// ExecDB executable (spec §4: "常に今動いているプロセス自身のエンジンで、
// データのみを読み込む"). Load does not warn about a footer
// format-version mismatch; call Inspect first if the caller wants that
// (spec §4).
//
// Load replaces the live database's content in place (via SQLite's
// online Backup API, see loadBlobInto) rather than swapping in a new
// keeper connection. This means any connection obtained before Load ran
// -- including a Session from a still-open pgwire/REPL client -- keeps
// working afterward and sees the newly loaded data, instead of failing
// mid-response because its underlying connection was closed out from
// under it.
func (db *DB) Load(path string) error {
	info, _, blob, err := loadFromFile(path)
	if err != nil {
		return err
	}
	if blob == nil {
		return fmt.Errorf("engine: %s: %w", path, ErrNoData)
	}

	db.mu.RLock()
	dsn := db.dsn
	closed := db.closed
	db.mu.RUnlock()
	if closed {
		return ErrClosed
	}

	if err := loadBlobInto(blob, dsn); err != nil {
		return fmt.Errorf("engine: %s: %w", path, err)
	}

	// info reflects which file this DB's data came from and is updated on
	// every successful Load. sourcePath/engineSize are deliberately left
	// untouched: they track the engine bytes a later Snapshot should
	// carry forward, which Load must never change (spec §4).
	db.mu.Lock()
	db.info = info
	db.mu.Unlock()
	return nil
}
