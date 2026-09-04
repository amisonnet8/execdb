package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// cmdDump implements ".dump [PATTERN]" (spec §3): every matching table's
// schema and data, plus the indexes/views/triggers that belong to those
// tables, rendered as SQL text to stdout wrapped in a transaction --
// piping the output back into a fresh ExecDB reproduces the database.
// PATTERN is a SQL LIKE pattern against table names (sqlite3's own
// ".dump" argument works the same way; ".schema" instead takes an exact
// name), defaulting to "%" (every table) when omitted.
//
// Literal values are rendered by SQLite's own quote() SQL function
// rather than a hand-rolled Go encoder: quote() already produces a
// correct SQL literal for every SQLite storage class (NULL -> the text
// NULL, INTEGER/REAL as-is, TEXT single-quoted with embedded quote
// characters doubled per SQL's own escaping convention, BLOB as X'..'
// hex) directly inside the database engine, where the original storage
// class is still known -- by the time a value reaches Go via Scan, TEXT
// and a BLOB's would-be string form are both just Go strings, too late
// to reliably tell apart.
func (r *repl) cmdDump(args []string) {
	pattern := "%"
	if len(args) > 0 {
		pattern = args[0]
	}

	fmt.Println("PRAGMA foreign_keys=OFF;")
	fmt.Println("BEGIN TRANSACTION;")

	if err := r.dumpTables(pattern); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	if err := r.dumpSequence(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	if err := r.dumpOtherObjects(pattern); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}

	fmt.Println("COMMIT;")
}

// dumpTables emits CREATE TABLE followed immediately by that table's own
// data (matching sqlite3's per-table interleaving) for every table whose
// name matches pattern, in sqlite_master's own (creation) order.
func (r *repl) dumpTables(pattern string) error {
	rows, err := r.sess.Query(
		`SELECT name, sql FROM sqlite_master
		 WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name LIKE ?`,
		pattern,
	)
	if err != nil {
		return err
	}
	type table struct{ name, sql string }
	var tables []table
	for rows.Next() {
		var name string
		var sqlText sql.NullString
		if err := rows.Scan(&name, &sqlText); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, table{name, sqlText.String})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close() // done with this Rows before issuing further queries on the same Session connection

	for _, t := range tables {
		fmt.Println(t.sql + ";")
		if err := r.dumpTableData(t.name); err != nil {
			return err
		}
	}
	return nil
}

func (r *repl) dumpTableData(name string) error {
	cols, err := r.tableColumns(name)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}

	exprs := make([]string, len(cols))
	for i, c := range cols {
		exprs[i] = "quote(" + quoteIdent(c) + ")"
	}
	query := "SELECT " + strings.Join(exprs, ", ") + " FROM " + quoteIdent(name)

	rows, err := r.sess.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	vals := make([]string, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	prefix := "INSERT INTO " + quoteIdent(name) + " VALUES("
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		fmt.Println(prefix + strings.Join(vals, ",") + ");")
	}
	return rows.Err()
}

// tableColumns returns name's columns in declaration order via the
// pragma_table_info table-valued function, which (unlike the classic
// "PRAGMA table_info(x)" statement form) accepts a normal bound
// parameter.
func (r *repl) tableColumns(name string) ([]string, error) {
	rows, err := r.sess.Query(`SELECT name FROM pragma_table_info(?)`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// dumpSequence restores AUTOINCREMENT counters, matching sqlite3's own
// dump: sqlite_sequence rows are data like any other, but skipping them
// entirely (dumpTables excludes every "sqlite_%" table) and then just
// re-inserting them here, unconditionally, is wrong when the table
// doesn't exist in this build (no AUTOINCREMENT column was ever used) or
// has no rows -- both checked below before emitting anything.
func (r *repl) dumpSequence() error {
	var exists int
	if err := r.sess.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'`,
	).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}

	rows, err := r.sess.Query(`SELECT quote(name), quote(seq) FROM sqlite_sequence`)
	if err != nil {
		return err
	}
	var lines []string
	for rows.Next() {
		var name, seq string
		if err := rows.Scan(&name, &seq); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, fmt.Sprintf("INSERT INTO %s VALUES(%s,%s);", quoteIdent("sqlite_sequence"), name, seq))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(lines) == 0 {
		return nil
	}
	fmt.Println("DELETE FROM sqlite_sequence;")
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

// dumpOtherObjects emits every index/view/trigger belonging to a table
// matching pattern, using sqlite_master's tbl_name column (the table an
// index/trigger is defined on, or a view's own name) rather than name,
// so a pattern-scoped dump of one table also carries the indexes and
// triggers defined on it. sql IS NOT NULL excludes implicit
// sqlite_autoindex_% entries (UNIQUE/PRIMARY KEY constraints), which
// have no CREATE statement of their own to replay.
func (r *repl) dumpOtherObjects(pattern string) error {
	rows, err := r.sess.Query(
		`SELECT sql FROM sqlite_master
		 WHERE sql IS NOT NULL AND type IN ('index', 'view', 'trigger')
		   AND tbl_name LIKE ? AND name NOT LIKE 'sqlite_%'`,
		pattern,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sqlText string
		if err := rows.Scan(&sqlText); err != nil {
			return err
		}
		fmt.Println(sqlText + ";")
	}
	return rows.Err()
}

// quoteIdent double-quotes a SQLite identifier (table/column name),
// doubling any embedded '"' -- the standard SQL escaping for a
// double-quoted identifier.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
