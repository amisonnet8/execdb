// Command execdb is the reference single-binary RDBMS built on top of the
// engine package: a REPL plus a PostgreSQL-compatible wire protocol server,
// with data embedded in the executable itself.
//
// See execdb_spec.md for the full specification.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/amisonnet8/execdb/engine"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// options holds the parsed startup flags (spec §9). password is not a
// flag -- it is resolved by resolveAuth (auth.go) from EXECDB_PASSWORD or
// an interactive prompt, never accepted on the command line (which would
// leak it via the process list / shell history).
type options struct {
	pgAddr           string
	socket           string
	snapshotAs       string
	noRepl           bool
	quiet            bool
	timestamp        bool
	snapshotInterval time.Duration
	user             string
	password         string
}

func parseFlags(args []string) (*options, error) {
	fs := flag.NewFlagSet("execdb", flag.ContinueOnError)
	opts := &options{}

	fs.StringVar(&opts.pgAddr, "pg-addr", "", "")
	fs.StringVar(&opts.pgAddr, "p", "", "")
	fs.StringVar(&opts.socket, "socket", "", "")
	fs.StringVar(&opts.socket, "s", "", "")
	fs.StringVar(&opts.snapshotAs, "snapshot-as", "", "")
	fs.StringVar(&opts.snapshotAs, "o", "", "")
	fs.BoolVar(&opts.noRepl, "no-repl", false, "")
	fs.BoolVar(&opts.noRepl, "n", false, "")
	fs.BoolVar(&opts.quiet, "quiet", false, "")
	fs.BoolVar(&opts.quiet, "q", false, "")
	fs.BoolVar(&opts.timestamp, "timestamp", false, "")
	fs.BoolVar(&opts.timestamp, "t", false, "")
	fs.DurationVar(&opts.snapshotInterval, "snapshot-interval", 0, "")
	fs.DurationVar(&opts.snapshotInterval, "i", 0, "")
	fs.StringVar(&opts.user, "user", "", "")
	fs.StringVar(&opts.user, "u", "", "")

	fs.Usage = printUsage
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return opts, nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `ExecDB v%s

Usage: execdb [options]

Options:
  -p, --pg-addr ADDR      PostgreSQL wire protocol TCP listen address (e.g. :5432)
  -s, --socket PATH       PostgreSQL wire protocol UNIX domain socket path
  -o, --snapshot-as NAME  Default filename for .snapshot / server-mode auto-save
  -n, --no-repl           Run in server mode without starting the REPL
  -q, --quiet             Suppress the startup banner
  -t, --timestamp         Append a timestamp to saved filenames
  -i, --snapshot-interval DURATION
                          Periodically save a snapshot at this interval (e.g. 5m)
  -u, --user NAME         Require this username + a password for the external I/F
                          (Zero-Auth if unset). Password: $EXECDB_PASSWORD, else an
                          interactive prompt in REPL mode (an error in -n mode)
  -h, --help              Show this message
`, version)
}

func main() {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}
	run(opts)
}

func run(opts *options) {
	if err := resolveAuth(opts); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	db, err := engine.OpenSelf()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	defer db.Close()

	stopPgwire, err := startPgwire(db, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	defer stopPgwire()

	if opts.snapshotInterval > 0 {
		stop := make(chan struct{})
		defer close(stop)
		go runSnapshotInterval(db, opts, stop)
	}

	printBanner(db, opts)

	if opts.noRepl {
		runServerMode(db, opts)
		return
	}
	runREPL(db, opts)
}

func printBanner(db *engine.DB, opts *options) {
	if opts.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, "ExecDB v%s\n", version)

	if info := db.Info(); info.HasData {
		fmt.Fprintf(os.Stderr, "Loaded snapshot: %s\n", filepath.Base(info.Path))
	} else {
		fmt.Fprintln(os.Stderr, "No embedded data. Starting with an empty in-memory database.")
	}

	if opts.pgAddr != "" {
		fmt.Fprintf(os.Stderr, "Listening on %s (PostgreSQL wire protocol)\n", opts.pgAddr)
	}
	if opts.socket != "" {
		fmt.Fprintf(os.Stderr, "Listening on %s (UNIX Domain Socket)\n", opts.socket)
	}
	if opts.user != "" && (opts.pgAddr != "" || opts.socket != "") {
		fmt.Fprintf(os.Stderr, "External I/F authentication required (user: %s)\n", opts.user)
	}

	if opts.noRepl {
		fmt.Fprintln(os.Stderr, "Running in server mode (--no-repl). Send SIGTERM to save and exit.")
	} else {
		fmt.Fprintln(os.Stderr, `Enter ".help" for usage hints.`)
	}
}

// runServerMode blocks until SIGTERM/SIGINT, then auto-saves and exits --
// the one exception to "no automatic saving" that server mode requires,
// since there is no REPL to run ".snapshot"/".overwrite" from
// (.claude/rules/cli-output.md).
func runServerMode(db *engine.DB, opts *options) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	autoSaveOnShutdown(db, opts)
}

func autoSaveOnShutdown(db *engine.DB, opts *options) {
	path := resolveSnapshotFilename(opts, "", opts.timestamp)
	if err := db.Snapshot(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error saving snapshot:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Saved snapshot to %s\n", path)
}

// runSnapshotInterval saves a snapshot every opts.snapshotInterval, in
// both REPL and server mode (spec §9: "REPLモード・サーバーモード
// いずれでも有効"), until stop is closed. A save failure (most notably
// engine.ErrBusy, a legitimate outcome of racing a concurrent writer --
// .claude/rules/sqlite-quirks.md, PLAN.md's phase 2 Step 6 note) is
// reported to stderr and just waits for the next tick, rather than
// treated as fatal: unlike server-mode's shutdown save, there will be
// another chance shortly.
func runSnapshotInterval(db *engine.DB, opts *options, stop <-chan struct{}) {
	ticker := time.NewTicker(opts.snapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			path := resolveSnapshotFilename(opts, "", opts.timestamp)
			if err := db.Snapshot(path); err != nil {
				fmt.Fprintln(os.Stderr, "Error saving periodic snapshot:", err)
				continue
			}
			fmt.Fprintf(os.Stderr, "Saved periodic snapshot to %s\n", path)
		}
	}
}

// defaultSnapshotBase is the base filename used when neither
// --snapshot-as nor an explicit ".snapshot" argument is given: the
// running binary's own name (spec §9).
func defaultSnapshotBase() string {
	self, err := os.Executable()
	if err != nil {
		return "execdb"
	}
	return filepath.Base(self)
}

// resolveSnapshotFilename applies naming.md's single file-naming rule
// (snapshotFilename) using the base-name precedence spec §9 defines: an
// explicit filename (e.g. a ".snapshot NAME" argument, or "" if none),
// else --snapshot-as, else the running binary's own name. Shared by
// .snapshot, server-mode's auto-save-on-shutdown, and
// --snapshot-interval's periodic saves, so this precedence and the
// naming rule itself exist in exactly one place.
func resolveSnapshotFilename(opts *options, explicit string, withTimestamp bool) string {
	base := explicit
	if base == "" {
		base = opts.snapshotAs
	}
	if base == "" {
		base = defaultSnapshotBase()
	}
	return snapshotFilename(base, withTimestamp, time.Now(), runtime.GOOS)
}
