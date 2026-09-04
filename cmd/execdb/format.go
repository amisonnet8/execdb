package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// printRows prints query results to stdout in sqlite3's default style
// (.claude/rules/cli-output.md: follow sqlite3's own output format when in
// doubt): one row per line, columns separated by "|", headers off, NULL
// rendered as an empty string.
func printRows(rows *sql.Rows) {
	cols, err := rows.Columns()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
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
