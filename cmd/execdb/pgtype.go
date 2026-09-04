package main

import (
	"database/sql"
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

// pgColumn describes one RowDescription field: its name and the OID chosen
// for it by columnOID below.
type pgColumn struct {
	name string
	oid  uint32
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
// (ScanType) only when there is no declared type at all (expression,
// aggregate, or literal columns -- decltype is "" for those, per the
// spike's findings). This split matters because DatabaseTypeName() is
// static schema information while ScanType() reflects one concrete sampled
// value, which is all that is available (and all that is needed) for a
// computed column.
func columnOID(ct *sql.ColumnType) uint32 {
	if decl := ct.DatabaseTypeName(); decl != "" {
		return affinityOID(decl)
	}
	return scanTypeOID(ct.ScanType())
}

// affinityOID maps a SQLite declared column type (decltype) to a Postgres
// OID. BOOLEAN and DATE/DATETIME/TIMESTAMP are special-cased ahead of
// SQLite's own 5-way type-affinity algorithm (sqlite.org/datatype3.html
// §3.1) because both fall into plain SQLite's catch-all NUMERIC affinity
// bucket, yet the spike found modernc.org/sqlite gives them observably
// different behavior worth a distinct OID (a DATE/DATETIME/TIME column
// scans as time.Time; a BOOLEAN column stores 0/1 as INTEGER but Postgres's
// bool text format is "t"/"f", handled by pgEncodeValue below). Neither
// substring overlaps SQLite's own affinity keywords (INT/CHAR/CLOB/TEXT/
// BLOB/REAL/FLOA/DOUB), so special-casing them first cannot shadow a
// legitimate affinity match.
func affinityOID(decl string) uint32 {
	u := strings.ToUpper(decl)
	switch {
	case strings.Contains(u, "BOOL"):
		return oidBool
	case strings.Contains(u, "DATE"), strings.Contains(u, "TIME"):
		return oidTimestamp
	case strings.Contains(u, "INT"):
		return oidInt8
	case strings.Contains(u, "CHAR"), strings.Contains(u, "CLOB"), strings.Contains(u, "TEXT"):
		return oidText
	case strings.Contains(u, "BLOB"):
		return oidBytea
	case strings.Contains(u, "REAL"), strings.Contains(u, "FLOA"), strings.Contains(u, "DOUB"):
		return oidFloat8
	default:
		return oidNumeric // SQLite's NUMERIC affinity catch-all (rule 5)
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

// pgEncodeValue renders one scanned column value as PostgreSQL's text wire
// format for oid, or nil for SQL NULL (DataRow's -1-length encoding,
// unchanged from before this file existed). Unlike format.go's formatValue
// (REPL/list-mode display), this always type-switches on the value's
// actual Go type from database/sql -- independent of what OID
// columnOID advertised for the column -- since the spike found that does
// not always agree (a BOOLEAN column's promised oidBool, for instance,
// still scans as int64(0/1), never Go bool; see columnOID's doc comment).
// The int64-for-bool mismatch is the one case handled explicitly below;
// any other declared-vs-actual mismatch (e.g. a value violating its
// column's declared type affinity) is sent using the value's own actual
// type, a known limitation documented in PLAN.md's phase 4 Step 1 notes.
func pgEncodeValue(oid uint32, v any) *string {
	if v == nil {
		return nil
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
