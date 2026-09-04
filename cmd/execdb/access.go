package main

import "strings"

// splitStatements splits sql into individual statements on top-level ";"
// boundaries, aware of single-quoted strings, double-quoted identifiers,
// and "--"/"/* */" comments, so a ";" inside any of those does not end a
// statement. Used both to classify every statement in a query for
// external-I/F access control (spec §2) and to execute a Simple Query's
// statements one at a time, matching real PostgreSQL's per-statement
// response behavior for a multi-statement message.
//
// Any trailing text after the last top-level ";" is included as a final
// statement if it is non-blank -- pgwire callers don't require a query to
// end with ";" (e.g. a bare "SELECT 1" is a complete Simple Query
// message). The REPL, which does care whether a statement is actually
// terminated yet, uses scanStatements directly instead.
func splitStatements(sql string) []string {
	stmts, remainder := scanStatements(sql)
	if strings.TrimSpace(remainder) != "" {
		stmts = append(stmts, remainder)
	}
	return stmts
}

// scanStatements is splitStatements' underlying scanner, exposing the
// distinction splitStatements collapses: complete holds every statement
// that was terminated by a top-level ";", and remainder holds whatever
// trailing text follows the last one (empty if the input ended exactly on
// a ";"). The REPL (repl.go) uses remainder to decide whether a
// statement is finished yet or more input needs to be read -- an
// unterminated quote or comment naturally leaves everything scanned so
// far in remainder, since the inner loops below only stop at a closing
// quote/comment marker or end of input.
func scanStatements(sql string) (complete []string, remainder string) {
	var stmts []string
	var cur strings.Builder

	runes := []rune(sql)
	n := len(runes)
	i := 0
	for i < n {
		c := runes[i]
		switch {
		case c == '\'':
			cur.WriteRune(c)
			i++
			for i < n {
				cur.WriteRune(runes[i])
				if runes[i] == '\'' {
					i++
					if i < n && runes[i] == '\'' { // escaped '' inside the string
						cur.WriteRune(runes[i])
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		case c == '"':
			cur.WriteRune(c)
			i++
			for i < n {
				cur.WriteRune(runes[i])
				if runes[i] == '"' {
					i++
					break
				}
				i++
			}
			continue
		case c == '-' && i+1 < n && runes[i+1] == '-':
			for i < n && runes[i] != '\n' {
				cur.WriteRune(runes[i])
				i++
			}
			continue
		case c == '/' && i+1 < n && runes[i+1] == '*':
			cur.WriteRune(runes[i])
			cur.WriteRune(runes[i+1])
			i += 2
			for i+1 < n && !(runes[i] == '*' && runes[i+1] == '/') {
				cur.WriteRune(runes[i])
				i++
			}
			if i+1 < n {
				cur.WriteRune(runes[i])
				cur.WriteRune(runes[i+1])
				i += 2
			} else {
				for i < n { // unterminated comment: consume to end
					cur.WriteRune(runes[i])
					i++
				}
			}
			continue
		case c == ';':
			stmts = append(stmts, cur.String())
			cur.Reset()
			i++
			continue
		default:
			cur.WriteRune(c)
			i++
		}
	}
	return stmts, cur.String()
}

// looksLikeRowReturning reports whether stmt is a statement that returns
// rows (so callers should use Query/QueryContext rather than
// Exec/ExecContext). This is a keyword check, not a real parser --
// adequate for the REPL and pgwire's row/no-row dispatch, both of which
// only need to pick the right database/sql method.
func looksLikeRowReturning(stmt string) bool {
	s := strings.TrimLeft(stmt, "( \t\r\n")
	switch firstKeyword(s) {
	case "SELECT", "PRAGMA", "EXPLAIN", "VALUES", "WITH":
		return true
	}
	return false
}

// firstKeyword returns the first SQL keyword in stmt, skipping leading
// whitespace and comments, uppercased for comparison.
func firstKeyword(stmt string) string {
	s := stmt
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if idx := strings.IndexByte(s, '\n'); idx >= 0 {
				s = s[idx+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = s[idx+2:]
				continue
			}
			return ""
		}
		break
	}
	i := 0
	for i < len(s) && isASCIILetter(s[i]) {
		i++
	}
	return strings.ToUpper(s[:i])
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// ddlKeywords are DDL per spec §2's classification.
var ddlKeywords = map[string]bool{
	"CREATE": true, "DROP": true, "ALTER": true, "TRUNCATE": true,
}

// otherRejectedKeywords is the "その他（外部I/Fでは常に拒否）" category
// added to spec §2 in phase 1 Step 4: statements that touch the
// filesystem (ATTACH) or process-wide/file-wide state (PRAGMA/VACUUM/
// REINDEX) in ways Zero-Auth must not expose to network clients, even
// though they are not DDL in the strict sense.
var otherRejectedKeywords = map[string]bool{
	"ATTACH": true, "DETACH": true, "PRAGMA": true, "VACUUM": true, "REINDEX": true,
}

// checkExternalAccess rejects DDL and the "other" category above for the
// external I/F (spec §2). It classifies every statement in sql, not just
// the first, so "SELECT 1; DROP TABLE t" cannot smuggle a DROP past the
// check by hiding it behind an allowed first statement -- SQLite executes
// multi-statement text, so a classifier that only looks at the first
// keyword can be bypassed this way.
func checkExternalAccess(sql string) error {
	for _, stmt := range splitStatements(sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		kw := firstKeyword(stmt)
		if ddlKeywords[kw] {
			return errDDLDenied
		}
		if otherRejectedKeywords[kw] {
			return &accessDeniedError{keyword: kw}
		}
	}
	return nil
}

var errDDLDenied = &accessDeniedError{message: "DDL statements are not allowed via external interface"}

// accessDeniedError formats the spec §8 error text: DDL keeps the exact
// wording spec §8 gives ("DDL statements..."), while the "other" category
// (added in Step 4) names the specific keyword instead, since spec §8
// only prescribes wording for the DDL case.
type accessDeniedError struct {
	keyword string
	message string
}

func (e *accessDeniedError) Error() string {
	if e.message != "" {
		return e.message
	}
	return e.keyword + " statements are not allowed via external interface"
}
