package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/amisonnet8/execdb/engine"
)

// isInteractive reports whether f looks like a terminal, per
// .claude/rules/naming.md's REPL design: a non-interactive (piped) stdin
// gets no prompts, so scripted input works cleanly.
func isInteractive(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

// runREPL reads SQL statements and dot-commands from stdin until EOF,
// ".exit"/".quit", or a successful ".overwrite" (spec §3, §4). SQL text
// accumulates across lines until a line ends with ";"; dot-commands are
// always a single line. Query results go to stdout; prompts, banners and
// errors go to stderr (.claude/rules/cli-output.md).
//
// The REPL holds one engine.Session for its entire run, the same
// independent-client model pgwire uses (spec §2/§8). This is not
// optional: database/sql's ResetSession does not roll back a transaction
// left open on a pooled connection (.claude/rules/sqlite-quirks.md), so
// running BEGIN through db.Exec's one-shot pooled connections would let
// COMMIT silently land on a different connection than BEGIN did.
func runREPL(db *engine.DB, opts *options) {
	sess, err := db.Session(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	defer sess.Close()

	interactive := isInteractive(os.Stdin)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var buf strings.Builder
	for {
		if interactive {
			if buf.Len() == 0 {
				fmt.Fprint(os.Stderr, "execdb> ")
			} else {
				fmt.Fprint(os.Stderr, "   ...> ")
			}
		}
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if buf.Len() == 0 && strings.HasPrefix(trimmed, ".") {
			if handleDotCommand(db, sess, opts, trimmed) {
				return
			}
			continue
		}
		if buf.Len() == 0 && trimmed == "" {
			continue
		}

		buf.WriteString(line)
		buf.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			execSQL(sess, buf.String())
			buf.Reset()
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

// execSQL runs one accumulated SQL statement, choosing Query over Exec
// for statements that return rows. This is a simple keyword check, not a
// real parser -- adequate for phase 1's minimal REPL.
func execSQL(sess *engine.Session, stmt string) {
	trimmed := strings.TrimSpace(stmt)
	if trimmed == "" {
		return
	}
	if looksLikeRowReturning(trimmed) {
		rows, err := sess.Query(stmt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		defer rows.Close()
		printRows(rows)
		return
	}
	if _, err := sess.Exec(stmt); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

func looksLikeRowReturning(stmt string) bool {
	upper := strings.ToUpper(strings.TrimLeft(stmt, "( \t\r\n"))
	for _, kw := range []string{"SELECT", "PRAGMA", "EXPLAIN", "VALUES", "WITH"} {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}

// handleDotCommand runs one dot-command and reports whether the REPL
// should now exit (".exit"/".quit", or a successful ".overwrite" -- spec
// §3, §4). Commands that just run SQL (.tables/.schema) go through sess,
// the REPL's own Session; .snapshot/.overwrite/.load are DB-level
// operations (they replace or persist the whole live database, not just
// run a statement on it) and go through db directly.
func handleDotCommand(db *engine.DB, sess *engine.Session, opts *options, line string) (exit bool) {
	fields := strings.Fields(line)
	cmd, args := fields[0], fields[1:]

	switch cmd {
	case ".exit", ".quit":
		return true
	case ".help":
		printHelp()
	case ".tables":
		cmdTables(sess)
	case ".schema":
		cmdSchema(sess, args)
	case ".snapshot":
		cmdSnapshot(db, opts, args)
	case ".overwrite":
		return cmdOverwrite(db)
	case ".load":
		cmdLoad(db, args)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q. Enter \".help\" for usage hints.\n", cmd)
	}
	return false
}

func printHelp() {
	fmt.Fprint(os.Stderr, `.exit                     Exit this program
.quit                     Exit this program (same as .exit)
.help                     Show this message
.tables                   List names of tables
.schema [TABLE]           Show CREATE statements
.snapshot [FILE] [--timestamp]
                          Write the current database to a new file
.overwrite                Overwrite the running executable with the current
                          database, then exit
.load FILE                Replace the in-memory database with the data
                          embedded in FILE
`)
}

func cmdTables(sess *engine.Session) {
	rows, err := sess.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		names = append(names, name)
	}
	if len(names) > 0 {
		fmt.Println(strings.Join(names, "  "))
	}
}

func cmdSchema(sess *engine.Session, args []string) {
	const base = `SELECT sql FROM sqlite_master WHERE sql IS NOT NULL AND `

	var rows *sql.Rows
	var err error
	if len(args) > 0 {
		rows, err = sess.Query(base+`name = ? ORDER BY type, name`, args[0])
	} else {
		rows, err = sess.Query(base + `name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sqlText string
		if err := rows.Scan(&sqlText); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		fmt.Println(sqlText + ";")
	}
}

func cmdSnapshot(db *engine.DB, opts *options, args []string) {
	filename := ""
	withTimestamp := opts.timestamp
	for _, a := range args {
		if a == "--timestamp" {
			withTimestamp = true
		} else if filename == "" {
			filename = a
		}
	}
	if filename == "" {
		filename = opts.snapshotAs
	}
	if filename == "" {
		filename = defaultSnapshotBase()
	}

	path := snapshotFilename(filename, withTimestamp, time.Now(), runtime.GOOS)
	if err := db.Snapshot(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", path)
}

func cmdOverwrite(db *engine.DB) bool {
	if err := db.Overwrite(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return false
	}
	fmt.Fprintln(os.Stderr, "Overwrote the running executable.")
	return true
}

func cmdLoad(db *engine.DB, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: .load FILENAME")
		return
	}
	path := args[0]

	// spec §4: a format-version mismatch is a warning, not a rejection,
	// and engine itself never logs -- this is the caller's job.
	if info, err := engine.Inspect(path); err == nil && info.HasData && info.Version != engine.FormatVersion {
		fmt.Fprintf(os.Stderr, "Warning: %s has ExecDB format version %d; this build is version %d.\n", path, info.Version, engine.FormatVersion)
	}

	if err := db.Load(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Loaded data from %s\n", path)
}
