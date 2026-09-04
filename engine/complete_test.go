package engine

import "testing"

func TestComplete(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"SELECT 1", false},
		{"SELECT 1;", true},
		{"SELECT 1; SELECT 2", false},
		{"SELECT 1; SELECT 2;", true},
		{"SELECT ';';", true},
		{"-- comment only\n", false},
		{"/* unterminated", false},

		// The case this API exists for: a CREATE TRIGGER body's
		// BEGIN...END must stay open across its own internal ";"
		// statement separators, and a CASE...END expression inside that
		// body must not be mistaken for the trigger's closing END.
		{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1", false},
		{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1;", false},
		{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END", false},
		{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END;", true},
		{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT CASE WHEN 1 THEN 2 ELSE 3 END", false},
		{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT CASE WHEN 1 THEN 2 ELSE 3 END; END;", true},
	}
	for _, c := range cases {
		got, err := Complete(c.sql)
		if err != nil {
			t.Errorf("Complete(%q) error = %v", c.sql, err)
			continue
		}
		if got != c.want {
			t.Errorf("Complete(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}
