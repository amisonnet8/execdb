package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFooterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image")

	engineBytes := []byte("ENGINEBYTES")
	data := []byte("hello data")
	blob := append(append([]byte{}, engineBytes...), data...)
	blob = append(blob, encodeFooter(int64(len(engineBytes)), int64(len(data)))...)
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !info.HasData {
		t.Fatal("expected HasData=true")
	}
	if info.Version != FormatVersion {
		t.Errorf("Version = %d, want %d", info.Version, FormatVersion)
	}
	if info.DataOffset != int64(len(engineBytes)) {
		t.Errorf("DataOffset = %d, want %d", info.DataOffset, len(engineBytes))
	}
	if info.DataLength != int64(len(data)) {
		t.Errorf("DataLength = %d, want %d", info.DataLength, len(data))
	}
}

func TestInspectNoMagicMeansNoData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain")
	content := []byte("just a plain binary with no ExecDB footer, long enough to hold one")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.HasData {
		t.Error("expected HasData=false for a file with no ExecDB magic")
	}
}

func TestInspectTooSmallMeansNoData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny")
	if err := os.WriteFile(path, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.HasData {
		t.Error("expected HasData=false for a file smaller than the footer")
	}
}

func TestInspectCorruptFooterIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt")

	blob := append([]byte("engine"), []byte("data")...)
	blob = append(blob, encodeFooter(6, 999)...) // claims a length the file doesn't have
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Inspect(path); err == nil {
		t.Error("expected an error for a footer whose offsets don't match the file size")
	}
}

func TestInspectNonexistentFile(t *testing.T) {
	if _, err := Inspect(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected an error for a nonexistent path")
	}
}
