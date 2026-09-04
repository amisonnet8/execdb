package main

import (
	"path"
	"regexp"
	"strings"
	"time"
)

const timestampLayout = "20060102150405"

var timestampSuffixPattern = regexp.MustCompile(`_[0-9]{14}$`)

// snapshotFilename applies the single file-naming rule in
// .claude/rules/naming.md for ".snapshot" / "--snapshot-as": if
// withTimestamp, strip any existing "_YYYYMMDDHHMMSS" suffix from base
// (deduping) and insert a fresh one before the extension; on goos
// "windows", append ".exe" if the extension was omitted. Both -o/-t on
// the CLI and the REPL's ".snapshot" command call this same function so
// the naming rule only exists in one place.
func snapshotFilename(base string, withTimestamp bool, now time.Time, goos string) string {
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)

	if withTimestamp {
		name = timestampSuffixPattern.ReplaceAllString(name, "")
		name = name + "_" + now.Format(timestampLayout)
	}

	if ext == "" && goos == "windows" {
		ext = ".exe"
	}
	return name + ext
}
