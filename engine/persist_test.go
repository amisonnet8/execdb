package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// serializedFixture returns a real SQLite serialization (spec §7) of a
// fresh database that has run ddl, for tests that need a valid data blob
// rather than an opaque placeholder -- Open()/Load() always Deserialize
// the data portion, so it must actually parse as SQLite even though the
// engine portion is never parsed at all.
func serializedFixture(t *testing.T, ddl string) []byte {
	t.Helper()
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	blob, err := db.serialize()
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestSnapshotThenOpenRoundTrip(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t(a INTEGER, b TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES (1, 'x'), (2, 'y')"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "snap.execdb")
	if err := db.Snapshot(path); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm()&0o111 != 0 {
		t.Errorf("expected a data-only Snapshot (no engine prefix) to be non-executable, got mode %v", stat.Mode())
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reopened.Close()

	rows, err := reopened.Query("SELECT a, b FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var a int
		var b string
		if err := rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d:%s", a, b))
	}
	if want := []string{"1:x", "2:y"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSnapshotPreservesEnginePrefix(t *testing.T) {
	// Simulate an ExecDB "executable" (engine bytes + data + footer),
	// modify it, and re-snapshot: the new file must still carry the
	// original engine bytes forward (spec §7). The engine prefix can be
	// arbitrary opaque bytes (never parsed as SQLite), but the data blob
	// must be a real serialization, since Open() will Deserialize it.
	dir := t.TempDir()
	original := filepath.Join(dir, "original")

	seedData := serializedFixture(t, "CREATE TABLE seed(x INTEGER)")
	engineBytes := []byte("PRETEND-ENGINE-BINARY-BYTES")
	if err := writeImageAtomic(original, engineBytes, seedData, 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := Open(original)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "renamed-copy")
	if err := db.Snapshot(out); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	stat, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no chmod-based execute bit -- a file's executability
	// there is determined by its extension (.exe etc), which is
	// cmd/execdb's responsibility (naming.md), not something engine's
	// os.Chmod(0o755) call can express. Only check the permission bit on
	// platforms where it means something.
	if runtime.GOOS != "windows" && stat.Mode().Perm()&0o100 == 0 {
		t.Errorf("expected an engine-carrying Snapshot to be executable, got mode %v", stat.Mode())
	}

	info, err := Inspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.DataOffset != int64(len(engineBytes)) {
		t.Errorf("DataOffset = %d, want %d (engine prefix not preserved)", info.DataOffset, len(engineBytes))
	}
	gotEngine, err := readEnginePrefix(out, info.DataOffset)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotEngine) != string(engineBytes) {
		t.Errorf("engine prefix mismatch: got %q, want %q", gotEngine, engineBytes)
	}
}

func TestLoadReplacesDataNotMerge(t *testing.T) {
	dbA, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	if _, err := dbA.Exec("CREATE TABLE original(a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbA.Exec("INSERT INTO original VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	dbB, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()
	if _, err := dbB.Exec("CREATE TABLE loaded(b TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbB.Exec("INSERT INTO loaded VALUES ('z')"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "b.execdb")
	if err := dbB.Snapshot(path); err != nil {
		t.Fatal(err)
	}

	if err := dbA.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := dbA.Exec("SELECT 1 FROM original"); err == nil {
		t.Error("expected dbA's own table to be gone after Load (Load replaces, does not merge)")
	}
	var s string
	if err := dbA.QueryRow("SELECT b FROM loaded").Scan(&s); err != nil {
		t.Fatalf("expected loaded's table to be present after Load: %v", err)
	}
	if s != "z" {
		t.Errorf("got %q, want %q", s, "z")
	}
}

func TestLoadOnFileWithoutDataIsError(t *testing.T) {
	db, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, []byte("no footer here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Load(path); err == nil {
		t.Error("expected Load to fail on a file with no ExecDB data")
	}
}

func TestLoadDoesNotChangeEnginePrefix(t *testing.T) {
	// spec §4: .load never references the source file's engine portion --
	// only its data -- even when the source is itself an ExecDB executable.
	dir := t.TempDir()

	selfLike := filepath.Join(dir, "self")
	selfEngineBytes := []byte("SELF-ENGINE")
	selfData := serializedFixture(t, "CREATE TABLE selfdata(x INTEGER)")
	if err := writeImageAtomic(selfLike, selfEngineBytes, selfData, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := Open(selfLike)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	other := filepath.Join(dir, "other")
	otherEngineBytes := []byte("OTHER-ENGINE-BYTES-OF-A-DIFFERENT-LENGTH")
	otherData := serializedFixture(t, "CREATE TABLE otherdata(y TEXT)")
	if err := writeImageAtomic(other, otherEngineBytes, otherData, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := db.Load(other); err != nil {
		t.Fatalf("Load: %v", err)
	}

	out := filepath.Join(dir, "after-load")
	if err := db.Snapshot(out); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.DataOffset != int64(len(selfEngineBytes)) {
		t.Errorf("DataOffset = %d, want %d (Load must not adopt the loaded file's engine size)", info.DataOffset, len(selfEngineBytes))
	}
	gotEngine, err := readEnginePrefix(out, info.DataOffset)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotEngine) != string(selfEngineBytes) {
		t.Errorf("engine prefix = %q, want the original self engine bytes %q", gotEngine, selfEngineBytes)
	}
}

func TestOverwriteSelfRenameDance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.WriteFile(path, []byte("old content"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := overwriteSelf(path, []byte("ENGINE"), []byte("DATA")); err != nil {
		t.Fatalf("overwriteSelf: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte("ENGINE"), []byte("DATA")...), encodeFooter(6, 4)...)
	if string(got) != string(want) {
		t.Error("content mismatch after overwriteSelf")
	}
	if _, err := os.Stat(path + oldSuffix); !os.IsNotExist(err) {
		t.Errorf("expected sidecar to be cleaned up, stat err = %v", err)
	}
}

func TestLooksLikeGoRunTempBinary(t *testing.T) {
	tmp := os.TempDir()
	goRunPath := filepath.Join(tmp, "go-build123456", "b001", "exe", "a.out")
	if !looksLikeGoRunTempBinary(goRunPath) {
		t.Errorf("expected %s to be detected as a go run temp binary", goRunPath)
	}
	if looksLikeGoRunTempBinary("/usr/local/bin/execdb") {
		t.Error("did not expect a normal installed path to be flagged")
	}
}

// TestOverwriteEndToEnd builds engine/testdata/overwritehelper to a real
// file (not a `go run` temp binary) and runs it twice, proving DB.Overwrite
// actually works against a live running executable -- the trickiest part
// of the whole design (PLAN.md: "早期に全体の中で動作確認する価値が高い").
func TestOverwriteEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a real binary; skipped with -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not found")
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	binPath := filepath.Join(t.TempDir(), "overwritehelper")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	build := exec.Command(goBin, "build", "-o", binPath, "./engine/testdata/overwritehelper")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building helper: %v\n%s", err, out)
	}

	if out, err := exec.Command(binPath, "seed").CombinedOutput(); err != nil {
		t.Fatalf("seed run: %v\n%s", err, out)
	}

	out, err := exec.Command(binPath, "read").CombinedOutput()
	if err != nil {
		t.Fatalf("read run: %v\n%s", err, out)
	}
	if got := string(out); got != "42\n" {
		t.Errorf("read output = %q, want %q", got, "42\n")
	}

	if _, err := os.Stat(binPath + oldSuffix); !os.IsNotExist(err) {
		t.Errorf("expected no leftover %s sidecar after Overwrite on %s", oldSuffix, runtime.GOOS)
	}
}
