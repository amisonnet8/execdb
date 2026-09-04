package engine

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	// Magic identifies an ExecDB footer at the end of a file (spec §7).
	Magic = "EXECDB01"
	// FooterSize is the fixed size, in bytes, of the trailing footer:
	// Magic(8) + Version(4) + DataOffset(8) + DataLength(8) + Reserved(4).
	// All integer fields are big-endian.
	FooterSize = 32
	// FormatVersion is the current data-blob format version written into
	// a footer's Version field.
	FormatVersion = 1
	// MaxDataSize is the largest data blob engine can hold. It matches
	// modernc.org/sqlite's memdb VFS default ceiling
	// (SQLITE_MEMDB_DEFAULT_MAXSIZE = 1GiB); Step 1's spike found
	// SQLITE_FULL in practice a bit below that, around 960MiB (PLAN.md
	// "フェーズ②Step 1で確定した事実", .claude/rules/sqlite-quirks.md). A
	// footer claiming a DataLength larger than this is necessarily
	// corrupt.
	MaxDataSize = 1 << 30
)

// Info describes what, if anything, an ExecDB footer says about a file.
type Info struct {
	Path       string
	HasData    bool
	Version    uint32
	DataOffset int64
	DataLength int64
}

// Inspect reads only the trailing footer of path, without loading any
// data into memory. Callers that need to warn about a format-version
// mismatch before calling Load use this (spec §4); engine itself never
// logs.
func Inspect(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{Path: path}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return Info{Path: path}, err
	}
	size := stat.Size()

	var footer []byte
	if size >= FooterSize {
		footer = make([]byte, FooterSize)
		if _, err := f.ReadAt(footer, size-FooterSize); err != nil {
			return Info{Path: path}, err
		}
	}

	info, err := decodeFooter(footer, size)
	info.Path = path
	if err != nil {
		return info, fmt.Errorf("engine: %s: %w", path, err)
	}
	return info, nil
}

// decodeFooter parses a FooterSize-byte trailing footer (already
// extracted from wherever it lives) against the total size, in bytes, of
// whatever it came from, and reports what it says. footer may be nil or
// shorter than FooterSize if size < FooterSize; a footer whose magic
// doesn't match, or a size too small to hold one, means "no data" rather
// than an error -- only a footer whose magic matches but whose fields are
// inconsistent with size is an error. Shared by Inspect (a file's footer)
// and LoadFrom (an io.Reader's, persist.go).
func decodeFooter(footer []byte, size int64) (Info, error) {
	if size < FooterSize || string(footer[0:8]) != Magic {
		return Info{}, nil
	}

	dataOffset := int64(binary.BigEndian.Uint64(footer[12:20]))
	dataLength := int64(binary.BigEndian.Uint64(footer[20:28]))
	if dataOffset < 0 || dataLength < 0 || dataLength > MaxDataSize || dataOffset+dataLength+FooterSize != size {
		return Info{}, fmt.Errorf("corrupt ExecDB footer (offset=%d length=%d size=%d)", dataOffset, dataLength, size)
	}

	return Info{
		HasData:    true,
		Version:    binary.BigEndian.Uint32(footer[8:12]),
		DataOffset: dataOffset,
		DataLength: dataLength,
	}, nil
}

// encodeFooter builds the trailing footer for an image whose engine
// prefix is dataOffset bytes long and whose data blob is dataLength bytes.
func encodeFooter(dataOffset, dataLength int64) []byte {
	footer := make([]byte, FooterSize)
	copy(footer[0:8], Magic)
	binary.BigEndian.PutUint32(footer[8:12], FormatVersion)
	binary.BigEndian.PutUint64(footer[12:20], uint64(dataOffset))
	binary.BigEndian.PutUint64(footer[20:28], uint64(dataLength))
	return footer
}
