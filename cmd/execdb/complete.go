package main

import (
	"strings"

	"github.com/amisonnet8/execdb/engine"
)

// completeStatements is scanStatements (access.go), refined to keep a
// CREATE TRIGGER body's BEGIN...END together as one statement instead of
// splitting at every top-level ";" scanStatements finds inside it.
// scanStatements already correctly skips a ";" inside a string/
// identifier literal or a comment; it just has no notion of compound
// statement bodies at all. Rather than teaching it BEGIN/END/CASE
// nesting by hand -- which risks exactly the mistake a CASE...END
// expression invites, since a naive counter that only understands
// "BEGIN"/"END" tokens cannot tell a CASE's END (no matching BEGIN of
// its own) from the one that actually closes the trigger body -- each of
// scanStatements' candidate ";" boundaries is checked against SQLite's
// own sqlite3_complete() (engine.Complete), the same oracle the real
// sqlite3 CLI shell uses for exactly this purpose. A candidate that is
// not yet complete per engine.Complete means that ";" fell inside an
// open trigger body; scanning continues to the next candidate instead of
// treating it as a statement boundary.
func completeStatements(sqlText string) (complete []string, remainder string) {
	pieces, tail := scanStatements(sqlText)

	var acc strings.Builder
	for _, piece := range pieces {
		if acc.Len() > 0 {
			acc.WriteString(";")
		}
		acc.WriteString(piece)

		ok, err := engine.Complete(acc.String() + ";")
		if err != nil || !ok {
			continue // still inside an open trigger body (or Complete itself failed -- treated the same: keep waiting rather than risk running a truncated statement)
		}
		complete = append(complete, acc.String())
		acc.Reset()
	}

	if acc.Len() == 0 {
		return complete, tail
	}
	if tail != "" {
		acc.WriteString(";")
		acc.WriteString(tail)
	}
	return complete, acc.String()
}
