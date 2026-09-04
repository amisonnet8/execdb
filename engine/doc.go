// Package engine provides ExecDB's in-memory SQL engine as a Go library.
//
// It wraps modernc.org/sqlite to offer an in-memory database that can be
// persisted either as a new executable (Snapshot) or by overwriting the
// host process's own executable in place (Overwrite). DB.Session hands
// out dedicated connections so independent clients (e.g. one per pgwire
// connection) can each hold their own transaction. The package does not
// itself import net or net/http; all network I/O (REPL, PostgreSQL wire
// protocol) is the responsibility of the caller (cmd/execdb).
//
// See execdb_spec.md §6-7 for the full design.
package engine
