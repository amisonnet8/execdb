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

// options holds the parsed startup flags (spec §9). -u/--user and
// -i/--snapshot-interval are deferred to later phases (they need
// golang.org/x/term and a ticker respectively) and are intentionally not
// defined here or shown in --help.
type options struct {
	pgAddr     string
	socket     string
	snapshotAs string
	noRepl     bool
	quiet      bool
	timestamp  bool
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
	base := opts.snapshotAs
	if base == "" {
		base = defaultSnapshotBase()
	}
	path := snapshotFilename(base, opts.timestamp, time.Now(), runtime.GOOS)
	if err := db.Snapshot(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error saving snapshot:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Saved snapshot to %s\n", path)
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
