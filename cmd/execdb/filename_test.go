package main

import (
	"testing"
	"time"
)

func TestSnapshotFilename(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	const ts = "20260901120000"

	cases := []struct {
		name          string
		base          string
		withTimestamp bool
		goos          string
		want          string
	}{
		{"no timestamp, linux, no ext", "mydb", false, "linux", "mydb"},
		{"no timestamp, windows, no ext gets .exe", "mydb", false, "windows", "mydb.exe"},
		{"no timestamp, has ext, unchanged", "mydb.exe", false, "windows", "mydb.exe"},
		{"timestamp, no ext", "mydb", true, "linux", "mydb_" + ts},
		{"timestamp, no ext, windows gets .exe", "mydb", true, "windows", "mydb_" + ts + ".exe"},
		{"timestamp, existing timestamp replaced", "mydb_20260101120000", true, "linux", "mydb_" + ts},
		{"timestamp, has ext", "mydb.exe", true, "linux", "mydb_" + ts + ".exe"},
		{"timestamp, existing timestamp and ext replaced", "mydb_20260101120000.exe", true, "linux", "mydb_" + ts + ".exe"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := snapshotFilename(c.base, c.withTimestamp, now, c.goos)
			if got != c.want {
				t.Errorf("snapshotFilename(%q, %v, _, %q) = %q, want %q", c.base, c.withTimestamp, c.goos, got, c.want)
			}
		})
	}
}
