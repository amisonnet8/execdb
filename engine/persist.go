package engine

import (
	"errors"
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

// image serializes the current DB state and re-reads this DB's engine
// prefix from its source file, if any.
func (db *DB) image() (engineBytes, data []byte, err error) {
	db.mu.RLock()
	sourcePath := db.sourcePath
	engineSize := db.engineSize
	db.mu.RUnlock()

	data, err = db.serialize()
	if err != nil {
		return nil, nil, err
	}
	engineBytes, err = readEnginePrefix(sourcePath, engineSize)
	if err != nil {
		return nil, nil, err
	}
	return engineBytes, data, nil
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

// ErrNotOverwritable is returned by Overwrite when the running process's
// own executable path looks like a `go run` temporary binary: `go run`
// deletes it as soon as the process exits, which would make Overwrite a
// no-op that looks like it succeeded.
var ErrNotOverwritable = errors.New("engine: running executable is not overwritable (looks like a `go run` temporary binary)")

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

	data, err := db.serialize()
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
func (db *DB) Load(path string) error {
	_, _, blob, err := loadFromFile(path)
	if err != nil {
		return err
	}
	if blob == nil {
		return fmt.Errorf("engine: %s: no ExecDB data found", path)
	}

	newSdb, newKeeper, err := newSharedCacheDB()
	if err != nil {
		return err
	}
	if err := deserializeInto(newKeeper, blob); err != nil {
		newKeeper.Close()
		newSdb.Close()
		return fmt.Errorf("engine: %s: %w", path, err)
	}

	db.mu.Lock()
	oldSdb, oldKeeper := db.sdb, db.keeper
	db.sdb, db.keeper = newSdb, newKeeper
	db.mu.Unlock()

	oldKeeper.Close()
	oldSdb.Close()
	return nil
}
