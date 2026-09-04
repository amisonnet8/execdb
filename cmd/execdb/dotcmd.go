package main

import (
	"errors"
	"strings"
)

// parseDotCommand splits a REPL dot-command line into its command name
// and arguments, sqlite3-shell style: arguments may be wrapped in '...'
// or "..." to include whitespace, and a backslash escapes the following
// character. This replaces a plain strings.Fields split, which could not
// express a filename containing a space (e.g. ".import 'my data.csv' t"
// or ".snapshot \"my db\"").
//
// line is assumed to already start with "." (the caller checks this
// before routing to a dot-command at all) and non-empty.
func parseDotCommand(line string) (cmd string, args []string, err error) {
	fields, err := splitDotCommandArgs(line)
	if err != nil {
		return "", nil, err
	}
	return fields[0], fields[1:], nil
}

// splitDotCommandArgs is a small hand-rolled word splitter, not a general
// shell parser: it only needs to understand whitespace, '...'/"..."
// quoting, and backslash escapes, which is exactly what sqlite3's own
// dot-command line splitter supports.
//
//   - Inside '...', every character is literal (no escape processing --
//     the same convention POSIX shells use for single quotes); the quotes
//     themselves are not part of the field.
//   - Inside "...", a backslash escapes the following character (so
//     `\"` can appear inside a double-quoted field); the quotes
//     themselves are not part of the field.
//   - Outside quotes, a backslash escapes the following character,
//     letting a literal space be embedded in an otherwise-unquoted field
//     (e.g. `.load a\ b.execdb`).
func splitDotCommandArgs(line string) ([]string, error) {
	var fields []string
	var cur strings.Builder
	inField := false

	runes := []rune(line)
	n := len(runes)
	i := 0
	for i < n {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t':
			if inField {
				fields = append(fields, cur.String())
				cur.Reset()
				inField = false
			}
			i++
		case c == '\'':
			inField = true
			i++
			for {
				if i >= n {
					return nil, errors.New("unterminated '")
				}
				if runes[i] == '\'' {
					i++
					break
				}
				cur.WriteRune(runes[i])
				i++
			}
		case c == '"':
			inField = true
			i++
			for {
				if i >= n {
					return nil, errors.New(`unterminated "`)
				}
				if runes[i] == '"' {
					i++
					break
				}
				if runes[i] == '\\' && i+1 < n {
					i++
				}
				cur.WriteRune(runes[i])
				i++
			}
		case c == '\\' && i+1 < n:
			inField = true
			cur.WriteRune(runes[i+1])
			i += 2
		default:
			inField = true
			cur.WriteRune(c)
			i++
		}
	}
	if inField {
		fields = append(fields, cur.String())
	}
	if len(fields) == 0 {
		return nil, errors.New("empty command")
	}
	return fields, nil
}
