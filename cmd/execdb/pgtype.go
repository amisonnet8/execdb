package main

import (
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// PostgreSQL type OIDs this package maps SQLite columns onto (spec §8's
// type mapping, phase 4 Step 1's spike -- PLAN.md's "フェーズ④Step 1で
// 確定した事実"). Chosen to match real PostgreSQL's own well-known OIDs so
// type-strict drivers (pgx, pgJDBC) recognize them without any ExecDB-side
// extension.
const (
	oidBool      uint32 = 16
	oidBytea     uint32 = 17
	oidInt8      uint32 = 20
	oidText      uint32 = 25
	oidFloat8    uint32 = 701
	oidTimestamp uint32 = 1114
	oidNumeric   uint32 = 1700
)

// pgColumn describes one RowDescription field: its name, the OID chosen
// for it by columnOID, and whether it will be sent (and must be decoded)
// in PostgreSQL's binary wire format rather than text (pgextended.go's
// buildResultColumns decides this per Bind's requested format codes;
// Simple Query's sendRows/sendDataRows, pgwire.go, never sets it -- real
// PostgreSQL's Simple Query protocol has no mechanism for a client to
// request binary at all).
type pgColumn struct {
	name   string
	oid    uint32
	binary bool
}

// binaryCapableOIDs is the set of OIDs pgEncodeValue can render in
// PostgreSQL's binary wire format (encodeBinary below). Confirmed via
// real pgx testing (phase 4 Step 5, PLAN.md's "フェーズ④Step 5" notes) to
// be exactly the OIDs pgx's default (Extended Query) mode itself requests
// binary format for -- text/numeric are not in this set because pgx
// itself requests text for them by default, and Postgres's binary NUMERIC
// format (base-10000 digit groups) is deliberately not implemented (see
// columnOID's NUMERIC-affinity fallback below).
var binaryCapableOIDs = map[uint32]bool{
	oidInt8: true, oidFloat8: true, oidBool: true, oidBytea: true, oidTimestamp: true,
}

// pgTypLen returns RowDescription's per-type "data type size" field for
// oid: the fixed byte width for a fixed-length type, or -1 for a
// variable-length one. Real PostgreSQL uses these exact widths for its own
// bool/int8/float8/timestamp; text/bytea/numeric are variable-length in
// PostgreSQL itself, so -1 is correct there too, not just a placeholder.
func pgTypLen(oid uint32) int16 {
	switch oid {
	case oidBool:
		return 1
	case oidInt8, oidFloat8, oidTimestamp:
		return 8
	default:
		return -1
	}
}

// columnOID picks the Postgres type OID for one result column, following
// PLAN.md's phase 4 Step 1 decision: prefer the column's declared SQLite
// type (DatabaseTypeName, i.e. its "decltype"), mapped through SQLite's own
// type-affinity rules; fall back to the sampled first row's Go value type
// (ScanType) when there is no declared type at all (expression, aggregate,
// or literal columns -- decltype is "" for those, per the spike's
// findings) OR when the declared type falls into SQLite's catch-all
// NUMERIC affinity bucket (affinityOID's matched == false, e.g. a plain
// "NUMERIC"/"DECIMAL(10,2)" column) -- SQLite always physically stores a
// NUMERIC-affinity value as either INTEGER or REAL internally (it has no
// true arbitrary-precision decimal type), so sampling the runtime Go type
// is both accurate and lets that column avoid oidNumeric entirely. That
// matters because real drivers (pgx confirmed via phase 4 Step 5's actual
// connection testing, PLAN.md) request PostgreSQL's binary NUMERIC wire
// format -- a nontrivial base-10000 digit-group encoding -- by default for
// a genuine numeric(1700) column, which this package deliberately does
// not implement; routing these columns to oidInt8/oidFloat8 instead (both
// binary-capable, see binaryCapableOIDs) sidesteps that entirely.
func columnOID(ct *sql.ColumnType) uint32 {
	if decl := ct.DatabaseTypeName(); decl != "" {
		if oid, matched := affinityOID(decl); matched {
			return oid
		}
	}
	return scanTypeOID(ct.ScanType())
}

// affinityOID maps a SQLite declared column type (decltype) to a Postgres
// OID, reporting matched == false for SQLite's NUMERIC-affinity catch-all
// (columnOID falls back to sampling the runtime value's Go type in that
// case instead of using a fixed OID -- see its doc comment). BOOLEAN and
// DATE/DATETIME/TIMESTAMP are special-cased ahead of SQLite's own 5-way
// type-affinity algorithm (sqlite.org/datatype3.html §3.1) because both
// fall into plain SQLite's NUMERIC affinity bucket, yet the spike found
// modernc.org/sqlite gives them observably different behavior worth a
// distinct OID (a DATE/DATETIME/TIME column scans as time.Time; a BOOLEAN
// column stores 0/1 as INTEGER but Postgres's bool format is "t"/"f" text
// or a single 0/1 byte in binary, handled by pgEncodeValue below). Neither
// substring overlaps SQLite's own affinity keywords (INT/CHAR/CLOB/TEXT/
// BLOB/REAL/FLOA/DOUB), so special-casing them first cannot shadow a
// legitimate affinity match.
func affinityOID(decl string) (oid uint32, matched bool) {
	u := strings.ToUpper(decl)
	switch {
	case strings.Contains(u, "BOOL"):
		return oidBool, true
	case strings.Contains(u, "DATE"), strings.Contains(u, "TIME"):
		return oidTimestamp, true
	case strings.Contains(u, "INT"):
		return oidInt8, true
	case strings.Contains(u, "CHAR"), strings.Contains(u, "CLOB"), strings.Contains(u, "TEXT"):
		return oidText, true
	case strings.Contains(u, "BLOB"):
		return oidBytea, true
	case strings.Contains(u, "REAL"), strings.Contains(u, "FLOA"), strings.Contains(u, "DOUB"):
		return oidFloat8, true
	default:
		return 0, false // SQLite's NUMERIC affinity catch-all (rule 5)
	}
}

// scanTypeOID maps a sampled Go value type (sql.ColumnType.ScanType(), or
// nil when no row was available to sample -- an empty result set with no
// declared type) to a Postgres OID, for columns with no declared SQLite
// type to consult. text is the safe fallback for both nil and any Go type
// this switch does not recognize.
func scanTypeOID(rt reflect.Type) uint32 {
	if rt == nil {
		return oidText
	}
	if rt == reflect.TypeOf(time.Time{}) {
		return oidTimestamp
	}
	switch rt.Kind() {
	case reflect.Int64:
		return oidInt8
	case reflect.Float64:
		return oidFloat8
	case reflect.Bool:
		return oidBool
	case reflect.Slice:
		if rt.Elem().Kind() == reflect.Uint8 {
			return oidBytea
		}
	}
	return oidText
}

// pgEncodeValue renders one scanned column value as PostgreSQL wire data
// for oid, or nil for SQL NULL (DataRow's -1-length encoding, unchanged
// from before this file existed). When useBinary is true and oid is in
// binaryCapableOIDs, it renders PostgreSQL's binary format instead of
// text -- the returned *string carries raw binary bytes (a Go string is
// just a byte sequence, so this needs no separate "binary value" type;
// writeDataRow, pgproto.go, sends it as-is either way). Both paths always
// type-switch on the value's actual Go type from database/sql --
// independent of what OID columnOID advertised for the column -- since
// the spike found that does not always agree (a BOOLEAN column's promised
// oidBool, for instance, still scans as int64(0/1), never Go bool; see
// columnOID's doc comment). The int64-for-bool mismatch is handled
// explicitly in both encodeBinary and the text switch below; any other
// declared-vs-actual mismatch (e.g. a value violating its column's
// declared type affinity) falls through to the value's own actual type in
// text form, a known limitation documented in PLAN.md's phase 4 Step 1
// notes -- encodeBinary reports ok == false for such a mismatch, and the
// caller here falls back to text rather than sending a malformed binary
// payload under a binary format code.
func pgEncodeValue(oid uint32, useBinary bool, v any) *string {
	if v == nil {
		return nil
	}
	if useBinary {
		if data, ok := encodeBinary(oid, v); ok {
			s := string(data)
			return &s
		}
	}
	var s string
	switch x := v.(type) {
	case []byte:
		s = "\\x" + hex.EncodeToString(x)
	case float64:
		s = formatPGFloat(x)
	case int64:
		if oid == oidBool {
			s = pgBoolText(x != 0)
		} else {
			s = strconv.FormatInt(x, 10)
		}
	case bool:
		s = pgBoolText(x)
	case time.Time:
		s = x.UTC().Format("2006-01-02 15:04:05")
	case string:
		s = x
	default:
		s = fmt.Sprint(x)
	}
	return &s
}

// encodeBinary renders v in PostgreSQL's binary wire format for oid,
// reporting ok == false if oid has no binary encoder (see
// binaryCapableOIDs) or v's actual Go type does not match what oid
// promised (an affinity violation -- pgEncodeValue falls back to text in
// that case, exactly like the equivalent text-side mismatches it already
// handles).
func encodeBinary(oid uint32, v any) (data []byte, ok bool) {
	switch oid {
	case oidInt8:
		if x, isInt64 := v.(int64); isInt64 {
			return encodeBinaryInt8(x), true
		}
	case oidFloat8:
		if x, isFloat64 := v.(float64); isFloat64 {
			return encodeBinaryFloat8(x), true
		}
	case oidBool:
		switch x := v.(type) {
		case int64:
			return encodeBinaryBool(x != 0), true
		case bool:
			return encodeBinaryBool(x), true
		}
	case oidBytea:
		if x, isBytes := v.([]byte); isBytes {
			return x, true // PostgreSQL's binary bytea format IS the raw bytes, unchanged
		}
	case oidTimestamp:
		if x, isTime := v.(time.Time); isTime {
			return encodeBinaryTimestamp(x), true
		}
	}
	return nil, false
}

func encodeBinaryInt8(v int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return b[:]
}

func encodeBinaryFloat8(v float64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
	return b[:]
}

func encodeBinaryBool(v bool) []byte {
	if v {
		return []byte{1}
	}
	return []byte{0}
}

// pgEpoch is PostgreSQL's own epoch for binary timestamp encoding
// (2000-01-01 00:00:00 UTC), not Unix's -- real PostgreSQL's protocol
// defines timestamp/timestamptz's binary format as microseconds relative
// to this date, not 1970-01-01.
var pgEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func encodeBinaryTimestamp(t time.Time) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(t.UTC().Sub(pgEpoch).Microseconds()))
	return b[:]
}

// formatPGFloat renders f the way real PostgreSQL's float8 text format
// does: shortest round-tripping decimal for finite values, and the three
// fixed spellings for the non-finite ones (which strconv.FormatFloat does
// not produce on its own -- it emits "NaN"/"+Inf"/"-Inf").
func formatPGFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	default:
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
}

func pgBoolText(b bool) string {
	if b {
		return "t"
	}
	return "f"
}
