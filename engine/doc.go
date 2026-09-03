// Package engine provides ExecDB's in-memory SQL engine as a Go library.
//
// It wraps modernc.org/sqlite to offer an in-memory database that can be
// persisted either as a new executable (Snapshot) or by overwriting the
// host process's own executable in place (Overwrite). The package has no
// dependency on net/net-http; all network I/O (REPL, PostgreSQL wire
// protocol) is the responsibility of the caller (cmd/execdb).
//
// See execdb_spec.md §6-7 for the full design.
package engine
