package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"strconv"
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

// repl holds everything one REPL run needs across the lifetime of the
// process: the DB-level handle (for .snapshot/.overwrite/.load, which
// replace or persist the whole live database), the one Session this REPL
// holds for its entire run (see runREPL's doc comment), and the parsed
// startup options (for defaults like --snapshot-as/--timestamp).
// Bundling these lets the dot-command handlers become methods instead of
// each needing db/sess/opts threaded through as separate parameters.
type repl struct {
	db          *engine.DB
	sess        *engine.Session
	opts        *options
	interactive bool
	mode        outputMode // .mode (format.go); zero value behaves as modeList
	headers     bool       // .headers
}

// runREPL reads SQL statements and dot-commands from stdin until EOF,
// ".exit"/".quit", or a successful ".overwrite" (spec §3, §4). SQL text
// accumulates across lines until scanStatements (access.go) reports no
// unterminated trailing text; dot-commands are always a single line.
// Query results go to stdout; prompts, banners and errors go to stderr
// (.claude/rules/cli-output.md).
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

	r := &repl{db: db, sess: sess, opts: opts, interactive: isInteractive(os.Stdin), mode: modeList}
	r.run()
}

func (r *repl) run() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var buf strings.Builder
	for {
		if r.interactive {
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
			if r.handleDotCommand(trimmed) {
				return
			}
			continue
		}
		if buf.Len() == 0 && trimmed == "" {
			continue
		}

		buf.WriteString(line)
		buf.WriteString("\n")

		// completeStatements (complete.go), not a bare ";" suffix check:
		// it understands string/identifier literals, comments, and (via
		// SQLite's own sqlite3_complete()) CREATE TRIGGER's BEGIN...END
		// body, so a ";" inside any of those does not falsely end -- or
		// falsely fail to end -- the buffered input. remainder is only
		// whitespace once every statement typed so far is properly
		// terminated.
		complete, remainder := completeStatements(buf.String())
		if strings.TrimSpace(remainder) != "" {
			continue
		}
		for _, stmt := range complete {
			r.execSQL(stmt)
		}
		buf.Reset()
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

// execSQL runs one statement, choosing Query over Exec for statements
// that return rows (access.go's looksLikeRowReturning, shared with
// pgwire's own dispatch).
func (r *repl) execSQL(stmt string) {
	trimmed := strings.TrimSpace(stmt)
	if trimmed == "" {
		return
	}
	if looksLikeRowReturning(trimmed) {
		rows, err := r.sess.Query(stmt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return
		}
		defer rows.Close()
		r.printRows(rows)
		return
	}
	if _, err := r.sess.Exec(stmt); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}

// handleDotCommand runs one dot-command and reports whether the REPL
// should now exit (".exit"/".quit" with no code, or a successful
// ".overwrite" -- spec §3, §4; ".exit CODE"/".quit CODE" terminate the
// process directly instead, see cmdExit). Commands that just run SQL
// (.tables/.schema) go through r.sess, the REPL's own Session;
// .snapshot/.overwrite/.load are DB-level operations (they replace or
// persist the whole live database, not just run a statement on it) and
// go through r.db directly.
func (r *repl) handleDotCommand(line string) (exit bool) {
	cmd, args, err := parseDotCommand(line)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return false
	}

	switch cmd {
	case ".exit", ".quit":
		return r.cmdExit(args)
	case ".help":
		printHelp()
	case ".mode":
		r.cmdMode(args)
	case ".headers":
		r.cmdHeaders(args)
	case ".tables":
		r.cmdTables()
	case ".schema":
		r.cmdSchema(args)
	case ".dump":
		r.cmdDump(args)
	case ".snapshot":
		r.cmdSnapshot(args)
	case ".overwrite":
		return r.cmdOverwrite()
	case ".load":
		r.cmdLoad(args)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q. Enter \".help\" for usage hints.\n", cmd)
	}
	return false
}

func printHelp() {
	fmt.Fprint(os.Stderr, `.exit [CODE]              Exit this program
.quit [CODE]              Exit this program (same as .exit)
.help                     Show this message
.mode MODE                Set output mode: list|column|csv|json|line
.headers on|off           Show column names in output
.tables                   List names of tables
.schema [TABLE]           Show CREATE statements
.dump [PATTERN]           Render the schema and data as SQL text
.snapshot [FILE] [--timestamp]
                          Write the current database to a new file
.overwrite                Overwrite the running executable with the current
                          database, then exit
.load FILE                Replace the in-memory database with the data
                          embedded in FILE
`)
}

// cmdExit implements ".exit"/".quit" (spec §3). With no argument it
// reports exit=true so run()'s caller unwinds normally (letting
// deferred cleanup -- Session.Close, DB.Close, stopPgwire -- run before
// the process exits 0). With an argument, sqlite3's own ".exit CODE"
// terminates the process immediately with that code instead, skipping
// further cleanup; ExecDB has nothing that must flush on exit (§1/§4:
// no automatic saving), so this is safe here too.
func (r *repl) cmdExit(args []string) (exit bool) {
	if len(args) == 0 {
		return true
	}
	code, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid exit code %q\n", args[0])
		return false
	}
	os.Exit(code)
	return true // unreachable
}

func (r *repl) cmdTables() {
	rows, err := r.sess.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
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

func (r *repl) cmdSchema(args []string) {
	const base = `SELECT sql FROM sqlite_master WHERE sql IS NOT NULL AND `

	var rows *sql.Rows
	var err error
	if len(args) > 0 {
		rows, err = r.sess.Query(base+`name = ? ORDER BY type, name`, args[0])
	} else {
		rows, err = r.sess.Query(base + `name NOT LIKE 'sqlite_%' ORDER BY type, name`)
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

// cmdMode implements ".mode MODE" (spec §3). Switching into column mode
// turns headers on automatically, matching sqlite3 -- a bare table of
// values with no header row is a lot less useful in column mode, where
// the whole point is readable alignment. headers can still be turned
// back off explicitly afterward.
func (r *repl) cmdMode(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: .mode MODE (list|column|csv|json|line)")
		return
	}
	mode := outputMode(strings.ToLower(args[0]))
	if !validOutputModes[mode] {
		fmt.Fprintf(os.Stderr, "Error: unknown mode %q. Available modes: list, column, csv, json, line.\n", args[0])
		return
	}
	r.mode = mode
	if mode == modeColumn {
		r.headers = true
	}
}

func (r *repl) cmdHeaders(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: .headers on|off")
		return
	}
	switch strings.ToLower(args[0]) {
	case "on":
		r.headers = true
	case "off":
		r.headers = false
	default:
		fmt.Fprintf(os.Stderr, "Error: expected \"on\" or \"off\", got %q\n", args[0])
	}
}

func (r *repl) cmdSnapshot(args []string) {
	filename := ""
	withTimestamp := r.opts.timestamp
	for _, a := range args {
		if a == "--timestamp" {
			withTimestamp = true
		} else if filename == "" {
			filename = a
		}
	}
	if filename == "" {
		filename = r.opts.snapshotAs
	}
	if filename == "" {
		filename = defaultSnapshotBase()
	}

	path := snapshotFilename(filename, withTimestamp, time.Now(), runtime.GOOS)
	if err := r.db.Snapshot(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", path)
}

func (r *repl) cmdOverwrite() bool {
	if err := r.db.Overwrite(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return false
	}
	fmt.Fprintln(os.Stderr, "Overwrote the running executable.")
	return true
}

func (r *repl) cmdLoad(args []string) {
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

	if err := r.db.Load(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Loaded data from %s\n", path)
}
