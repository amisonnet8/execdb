package main

import "testing"

func TestCompleteStatements(t *testing.T) {
	cases := []struct {
		in           string
		wantComplete []string
		wantRemain   string
	}{
		{"SELECT 1", nil, "SELECT 1"},
		{"SELECT 1;", []string{"SELECT 1"}, ""},
		{"SELECT 1; SELECT 2", []string{"SELECT 1"}, " SELECT 2"},
		{"SELECT 1;SELECT 2;", []string{"SELECT 1", "SELECT 2"}, ""},

		// The case this file exists for: a trigger body's internal ";"
		// must not be treated as a statement boundary, even when
		// scanStatements' naive candidate split lands right in the
		// middle of it.
		{
			"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END;",
			[]string{"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END"},
			"",
		},
		{
			"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END",
			nil,
			"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END",
		},
		{
			"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT CASE WHEN 1 THEN 2 ELSE 3 END; END;\nSELECT 1;",
			[]string{
				"CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT CASE WHEN 1 THEN 2 ELSE 3 END; END",
				"\nSELECT 1",
			},
			"",
		},
	}
	for _, c := range cases {
		gotComplete, gotRemain := completeStatements(c.in)
		if len(gotComplete) != len(c.wantComplete) {
			t.Fatalf("completeStatements(%q) complete = %v, want %v", c.in, gotComplete, c.wantComplete)
		}
		for i := range gotComplete {
			if gotComplete[i] != c.wantComplete[i] {
				t.Errorf("completeStatements(%q) complete[%d] = %q, want %q", c.in, i, gotComplete[i], c.wantComplete[i])
			}
		}
		if gotRemain != c.wantRemain {
			t.Errorf("completeStatements(%q) remainder = %q, want %q", c.in, gotRemain, c.wantRemain)
		}
	}
}
