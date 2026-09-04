package engine

import (
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// Complete reports whether sql is a syntactically complete SQL statement
// (or a semicolon-separated sequence of them), by calling SQLite's own
// sqlite3_complete() C routine -- the same one the real sqlite3 CLI
// shell uses to decide, while reading interactive input, whether to run
// what has been typed so far or keep waiting for more lines. A caller
// that reads SQL interactively (cmd/execdb's REPL, spec §3) needs this
// exact question answered correctly for a CREATE TRIGGER's BEGIN...END
// body: a hand-rolled scanner that just tracks "BEGIN"/"END" token
// nesting would be fooled by a CASE...END expression inside that body
// (CASE's END does not close a trigger body, but such a scanner has no
// way to tell the two apart), where sqlite3_complete's real tokenizer
// does not make that mistake.
//
// Complete does not touch any database -- sqlite3_complete is a pure
// text-scanning routine that takes no DB handle -- so this works without
// an open DB (New/Open/OpenSelf) by design.
//
// Reaching sqlite3_complete requires calling into modernc.org/sqlite's
// generated internals (modernc.org/sqlite/lib, not the stable driver
// package) rather than a documented public API; see
// .claude/rules/sqlite-quirks.md for what to recheck if
// modernc.org/sqlite is ever upgraded. modernc.org/libc and
// modernc.org/sqlite/lib are already transitive dependencies of
// modernc.org/sqlite itself (.claude/rules/binary-size.md,
// check-deps' "net" note in the Makefile), so this adds no new
// third-party dependency.
func Complete(sql string) (bool, error) {
	tls := libc.NewTLS()
	defer tls.Close()

	csql, err := libc.CString(sql)
	if err != nil {
		return false, err
	}
	defer libc.Xfree(tls, csql)

	return sqlite3.Xsqlite3_complete(tls, csql) != 0, nil
}
