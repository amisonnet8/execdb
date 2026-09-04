package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// outputMode is a REPL display mode (spec §3 ".mode"). The set is
// deliberately smaller than sqlite3's full list (which also has
// quote/insert/tabs/markdown/box/html): .claude/rules/cli-output.md rules
// out decorative modes in favor of plain, pipe-friendly output, and the
// remaining ones (quote/insert/tabs) were judged not worth the extra
// surface for phase 3 -- see PLAN.md's phase 3 scope decisions.
type outputMode string

const (
	modeList   outputMode = "list"
	modeColumn outputMode = "column"
	modeCSV    outputMode = "csv"
	modeJSON   outputMode = "json"
	modeLine   outputMode = "line"
)

var validOutputModes = map[outputMode]bool{
	modeList: true, modeColumn: true, modeCSV: true, modeJSON: true, modeLine: true,
}

// printRows renders a query's rows to stdout in r's current mode
// (.claude/rules/cli-output.md: follow sqlite3's own output format when
// in doubt).
func (r *repl) printRows(rows *sql.Rows) {
	cols, err := rows.Columns()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}

	switch r.mode {
	case modeColumn:
		r.printRowsColumn(rows, cols)
	case modeCSV:
		printRowsCSV(rows, cols, r.headers)
	case modeJSON:
		printRowsJSON(rows, cols)
	case modeLine:
		printRowsLine(rows, cols)
	default: // modeList
		printRowsList(rows, cols, r.headers)
	}
}

// printRowsList renders sqlite3's default "list" style: one row per
// line, columns separated by "|", NULL as an empty string. This is
// phase 1's original (and still the default) output format.
func printRowsList(rows *sql.Rows, cols []string, headers bool) {
	if headers {
		fmt.Println(strings.Join(cols, "|"))
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		fields := make([]string, len(cols))
		for i, v := range vals {
			fields[i] = formatValue(v)
		}
		fmt.Println(strings.Join(fields, "|"))
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

// printRowsCSV renders RFC 4180 CSV, matching sqlite3's ".mode csv":
// comma-separated, CRLF row terminator, NULL as an empty field.
func printRowsCSV(rows *sql.Rows, cols []string, headers bool) {
	w := csv.NewWriter(os.Stdout)
	w.UseCRLF = true
	defer w.Flush()

	if headers {
		if err := w.Write(cols); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	record := make([]string, len(cols))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		for i, v := range vals {
			record[i] = formatValue(v)
		}
		if err := w.Write(record); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

// printRowsLine renders sqlite3's ".mode line": each column of a row on
// its own "name = value" line, column names right-justified to the
// widest name so the "=" signs line up, and a blank line between rows.
func printRowsLine(rows *sql.Rows, cols []string) {
	maxLen := 0
	for _, c := range cols {
		if len(c) > maxLen {
			maxLen = len(c)
		}
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		for i, v := range vals {
			fmt.Printf("%*s = %s\n", maxLen, cols[i], formatValue(v))
		}
		fmt.Println()
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

// printRowsColumn renders sqlite3's ".mode column": left-justified,
// space-padded columns sized to their widest value, with an optional
// header row followed by a "-" underline. Unlike the other modes, this
// one must see every row before it can print the first one (column
// widths depend on the whole result set) -- acceptable here because
// ExecDB is an in-memory database with an ~1GiB ceiling
// (.claude/rules/sqlite-quirks.md), so a query's entire result set was
// already going to fit in memory as the live database itself.
func (r *repl) printRowsColumn(rows *sql.Rows, cols []string) {
	buffered, err := bufferRows(rows, len(cols))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}

	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	rendered := make([][]string, len(buffered))
	for i, vals := range buffered {
		rendered[i] = make([]string, len(cols))
		for j, v := range vals {
			s := formatValue(v)
			rendered[i][j] = s
			if len(s) > widths[j] {
				widths[j] = len(s)
			}
		}
	}

	if r.headers {
		printColumnRow(cols, widths)
		underline := make([]string, len(cols))
		for i, w := range widths {
			underline[i] = strings.Repeat("-", w)
		}
		printColumnRow(underline, widths)
	}
	for _, row := range rendered {
		printColumnRow(row, widths)
	}
}

func printColumnRow(fields []string, widths []int) {
	parts := make([]string, len(fields))
	for i, f := range fields {
		if i == len(fields)-1 {
			parts[i] = f // no trailing padding after the last column
		} else {
			parts[i] = fmt.Sprintf("%-*s", widths[i], f)
		}
	}
	fmt.Println(strings.Join(parts, "  "))
}

func bufferRows(rows *sql.Rows, ncols int) ([][]any, error) {
	var out [][]any
	for rows.Next() {
		vals := make([]any, ncols)
		ptrs := make([]any, ncols)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// printRowsJSON renders an array of one JSON object per row, column
// names as keys in their query order (a Go map would not preserve
// this, hence the manual object construction below rather than
// json.Marshal on a map[string]any per row).
func printRowsJSON(rows *sql.Rows, cols []string) {
	keys := make([]string, len(cols))
	for i, c := range cols {
		b, _ := json.Marshal(c)
		keys[i] = string(b)
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	fmt.Print("[")
	first := true
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		if !first {
			fmt.Print(",")
		}
		first = false

		fmt.Print("{")
		for i, v := range vals {
			if i > 0 {
				fmt.Print(",")
			}
			fmt.Printf("%s:%s", keys[i], jsonScalar(v))
		}
		fmt.Print("}")
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	fmt.Println("]")
}

// jsonScalar renders one scanned column value as a JSON scalar. NULL
// becomes null; INTEGER/REAL (int64/float64, per database/sql's driver
// mapping) become JSON numbers; TEXT (a Go string) becomes a JSON
// string. A BLOB ([]byte) is hex-encoded rather than embedded as a raw
// string: JSON has no binary type, and encoding/json would otherwise
// silently mangle non-UTF-8 bytes (replacing them with U+FFFD) instead
// of erroring, corrupting the data rather than just looking odd.
func jsonScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case []byte:
		b, _ := json.Marshal(hex.EncodeToString(x))
		return string(b)
	case int64, float64:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		b, _ := json.Marshal(fmt.Sprint(x))
		return string(b)
	}
}

// formatValue renders a single scanned column value as plain text.
// Shared by the "list"/"column"/"line"/"csv" REPL modes above and by
// pgwire's DataRow output (cmd/execdb/pgwire.go), which always sends
// text-format values regardless of the REPL's own display mode (spec
// §8's type mapping is still phase 4 future work -- see
// .claude/rules/pgwire.md).
func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}
