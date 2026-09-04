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
	info := Info{Path: path}

	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return info, err
	}
	size := stat.Size()
	if size < FooterSize {
		return info, nil // too small to hold a footer: no data
	}

	footer := make([]byte, FooterSize)
	if _, err := f.ReadAt(footer, size-FooterSize); err != nil {
		return info, err
	}
	if string(footer[0:8]) != Magic {
		return info, nil // an engine-only binary, or a foreign file
	}

	dataOffset := int64(binary.BigEndian.Uint64(footer[12:20]))
	dataLength := int64(binary.BigEndian.Uint64(footer[20:28]))
	if dataOffset < 0 || dataLength < 0 || dataOffset+dataLength+FooterSize != size {
		return info, fmt.Errorf("engine: %s: corrupt ExecDB footer (offset=%d length=%d file size=%d)", path, dataOffset, dataLength, size)
	}

	info.HasData = true
	info.Version = binary.BigEndian.Uint32(footer[8:12])
	info.DataOffset = dataOffset
	info.DataLength = dataLength
	return info, nil
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
