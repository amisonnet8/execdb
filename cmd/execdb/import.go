package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
)

// cmdImport implements ".import FILE TABLE" (spec §3): bulk-loads a CSV
// file into TABLE, creating it (all-TEXT columns, named from the file's
// own header row) if it doesn't already exist yet. Unlike sqlite3's own
// ".import", which reads whatever format ".mode" is currently set to
// (csv/tabs/ascii/...), ExecDB's .import always reads CSV -- .mode
// controls query *output*, and coupling a bulk-load command's *input*
// format to it would be an easy-to-miss surprise for a command whose
// whole point is "load exactly this file" (an intentional difference
// from sqlite3, spec §3).
func (r *repl) cmdImport(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: .import FILE TABLE")
		return
	}
	path, table := args[0], args[1]

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	defer f.Close()

	cr := csv.NewReader(bufio.NewReader(f))
	cr.FieldsPerRecord = -1 // field-count mismatches are checked by hand below, against the target table's own column count rather than just the file's first row

	exists, err := r.tableExists(table)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}

	var cols []string
	headerLines := 0
	if exists {
		cols, err = r.tableColumns(table)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
	} else {
		header, herr := cr.Read()
		if herr == io.EOF {
			fmt.Fprintf(os.Stderr, "Error: %s is empty; no header row to create %s from\n", path, quoteIdent(table))
			return
		}
		if herr != nil {
			fmt.Fprintln(os.Stderr, "Error reading header:", herr)
			return
		}
		cols = header
		headerLines = 1
		if err := r.createImportTable(table, cols); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
	}

	n, err := r.importRows(cr, table, cols, headerLines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Inserted %d rows into %s.\n", n, quoteIdent(table))
}

func (r *repl) tableExists(name string) (bool, error) {
	var count int
	err := r.sess.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&count)
	return count > 0, err
}

func (r *repl) createImportTable(table string, cols []string) error {
	colDefs := make([]string, len(cols))
	for i, c := range cols {
		colDefs[i] = quoteIdent(c) + " TEXT"
	}
	_, err := r.sess.Exec("CREATE TABLE " + quoteIdent(table) + "(" + strings.Join(colDefs, ",") + ")")
	return err
}

// importRows wraps the actual row-by-row insert in a SAVEPOINT rather
// than a plain BEGIN/COMMIT, so ".import" doesn't break when the user
// already has a transaction open on this Session (a bare BEGIN would
// fail with "cannot start a transaction within a transaction"; a
// savepoint nests cleanly either way). A failure partway through rolls
// back only this savepoint, leaving nothing from the failed import
// behind -- but leaving any transaction the user already had open
// exactly as it was.
func (r *repl) importRows(cr *csv.Reader, table string, cols []string, headerLines int) (int, error) {
	if _, err := r.sess.Exec("SAVEPOINT execdb_import"); err != nil {
		return 0, err
	}

	n, err := r.insertImportRows(cr, table, cols, headerLines)
	if err != nil {
		r.sess.Exec("ROLLBACK TO execdb_import")
		r.sess.Exec("RELEASE execdb_import")
		return 0, err
	}

	if _, err := r.sess.Exec("RELEASE execdb_import"); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *repl) insertImportRows(cr *csv.Reader, table string, cols []string, headerLines int) (int, error) {
	colList := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, c := range cols {
		colList[i] = quoteIdent(c)
		placeholders[i] = "?"
	}
	insertSQL := "INSERT INTO " + quoteIdent(table) + "(" + strings.Join(colList, ",") + ") VALUES(" + strings.Join(placeholders, ",") + ")"

	stmt, err := r.sess.Prepare(insertSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	n := 0
	lineNo := headerLines
	for {
		lineNo++
		record, err := cr.Read()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, fmt.Errorf("row %d: %w", lineNo, err)
		}
		if len(record) != len(cols) {
			return n, fmt.Errorf("row %d: expected %d fields, got %d", lineNo, len(cols), len(record))
		}

		args := make([]any, len(record))
		for i, v := range record {
			args[i] = v
		}
		if _, err := stmt.Exec(args...); err != nil {
			return n, fmt.Errorf("row %d: %w", lineNo, err)
		}
		n++
	}
}
