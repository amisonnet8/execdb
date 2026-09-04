package main

import (
	"io"
	"regexp"
	"strings"
)

// sessionParams holds the session-scoped values a pgwire connection has SET
// (spec §2's third response category, see handleSessionCommand below).
// Simple Query is handled synchronously by a single goroutine per
// connection (handleConnection), so this needs no locking.
type sessionParams map[string]string

// newSessionParams seeds a connection's sessionParams from seed (the same
// values sendStartupResponse reports via ParameterStatus, pgwire.go), so a
// SHOW before any SET reports what the client was already told at startup.
func newSessionParams(seed [][2]string) sessionParams {
	p := make(sessionParams, len(seed))
	for _, kv := range seed {
		p[strings.ToLower(kv[0])] = kv[1]
	}
	return p
}

// setStmtRe matches "SET [SESSION|LOCAL] name (TO|=) value". ExecDB does
// not distinguish SESSION/LOCAL scope (ExecDB has no sub-transaction
// parameter stack) -- the optional keyword is recognized only so it does
// not break the match, then discarded.
var setStmtRe = regexp.MustCompile(`(?is)^SET\s+(?:SESSION\s+|LOCAL\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:TO|=)\s*(.+?)\s*$`)

// showStmtRe matches "SHOW name". "SHOW ALL" (real PostgreSQL's
// list-every-setting form) is not implemented -- PLAN.md's phase 4 Step 3
// notes it as deferred until a real driver is observed sending it (Step 7),
// matching .claude/rules/pgwire.md's "don't widen scope ahead of a measured
// need" rule.
var showStmtRe = regexp.MustCompile(`(?is)^SHOW\s+([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// handleSessionCommand intercepts SET/SHOW before they ever reach SQLite:
// SQLite has no SET/SHOW of its own and would reject them as syntax
// errors, and pgJDBC in particular sends "SET extra_float_digits = 3"
// immediately on connect, so leaving these unhandled breaks that driver's
// connection outright (PLAN.md's phase 4 "P4-2"). This is neither the
// "reject" nor the "pass through to SQLite" outcome checkExternalAccess
// (access.go) produces for spec §2's DDL/"other" categories -- it is a
// third category ExecDB answers itself, which is why it is handled here in
// pgwire.go's dispatch loop rather than folded into access.go's
// classifier (which stays a pure "is this statement meant for SQLite"
// check). It reports whether kw was one of these (handled == true) and, if
// so, has already written the appropriate response.
func handleSessionCommand(w io.Writer, params sessionParams, kw, stmt string) (handled bool, err error) {
	switch kw {
	case "SET":
		return true, handleSet(w, params, stmt)
	case "SHOW":
		return true, handleShow(w, params, stmt)
	}
	return false, nil
}

func handleSet(w io.Writer, params sessionParams, stmt string) error {
	m := setStmtRe.FindStringSubmatch(stmt)
	if m == nil {
		return writeErrorResponse(w, sqlstateGeneric, "unsupported SET syntax: "+stmt)
	}
	params[strings.ToLower(m[1])] = unquoteSetValue(m[2])
	return writeCommandComplete(w, "SET")
}

func handleShow(w io.Writer, params sessionParams, stmt string) error {
	m := showStmtRe.FindStringSubmatch(stmt)
	if m == nil {
		return writeErrorResponse(w, sqlstateGeneric, "unsupported SHOW syntax: "+stmt)
	}
	name := strings.ToLower(m[1])
	if name == "all" {
		// "ALL" is real PostgreSQL's list-every-setting form, matched
		// here as an ordinary identifier by showStmtRe but not a real
		// parameter name -- reject it explicitly rather than silently
		// answering as if "all" were a settable parameter (see
		// showStmtRe's doc comment for why this is deferred, not
		// implemented).
		return writeErrorResponse(w, sqlstateGeneric, "SHOW ALL is not supported")
	}
	value := params[name] // "" (an empty text field, not NULL) if unset

	if err := writeRowDescription(w, []pgColumn{{name: name, oid: oidText}}); err != nil {
		return err
	}
	if err := writeDataRow(w, []*string{&value}); err != nil {
		return err
	}
	return writeCommandComplete(w, "SHOW")
}

// unquoteSetValue strips a single-quoted SET value down to its content,
// unescaping doubled ” the same way SQL string literals do (access.go's
// scanStatements applies the identical rule); a bare (unquoted) value --
// an identifier like "on"/"utf8" or a number -- is returned unchanged.
func unquoteSetValue(raw string) string {
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'")
	}
	return raw
}
