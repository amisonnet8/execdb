package main

import "strings"

// splitStatements splits sql into individual statements on top-level ";"
// boundaries, aware of single-quoted strings, double-quoted identifiers,
// and "--"/"/* */" comments, so a ";" inside any of those does not end a
// statement. Used both to classify every statement in a query for
// external-I/F access control (spec §2) and to execute a Simple Query's
// statements one at a time, matching real PostgreSQL's per-statement
// response behavior for a multi-statement message.
func splitStatements(sql string) []string {
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
	if strings.TrimSpace(cur.String()) != "" {
		stmts = append(stmts, cur.String())
	}
	return stmts
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
