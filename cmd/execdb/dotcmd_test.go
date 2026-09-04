package main

import "testing"

func TestParseDotCommand(t *testing.T) {
	cases := []struct {
		in       string
		wantCmd  string
		wantArgs []string
	}{
		{".tables", ".tables", nil},
		{".snapshot foo.execdb --timestamp", ".snapshot", []string{"foo.execdb", "--timestamp"}},
		{".import 'my data.csv' t", ".import", []string{"my data.csv", "t"}},
		{`.snapshot "my db"`, ".snapshot", []string{"my db"}},
		{`.import "a\"b.csv" t`, ".import", []string{`a"b.csv`, "t"}},
		{`.load a\ b.execdb`, ".load", []string{"a b.execdb"}},
		{".schema   t", ".schema", []string{"t"}},
		{".", ".", nil},
	}
	for _, c := range cases {
		cmd, args, err := parseDotCommand(c.in)
		if err != nil {
			t.Errorf("parseDotCommand(%q) error = %v, want nil", c.in, err)
			continue
		}
		if cmd != c.wantCmd {
			t.Errorf("parseDotCommand(%q) cmd = %q, want %q", c.in, cmd, c.wantCmd)
		}
		if len(args) != len(c.wantArgs) {
			t.Fatalf("parseDotCommand(%q) args = %v, want %v", c.in, args, c.wantArgs)
		}
		for i := range args {
			if args[i] != c.wantArgs[i] {
				t.Errorf("parseDotCommand(%q) args[%d] = %q, want %q", c.in, i, args[i], c.wantArgs[i])
			}
		}
	}
}

func TestParseDotCommandUnterminatedQuotes(t *testing.T) {
	cases := []string{
		`.snapshot 'unterminated`,
		`.snapshot "unterminated`,
	}
	for _, in := range cases {
		if _, _, err := parseDotCommand(in); err == nil {
			t.Errorf("parseDotCommand(%q) error = nil, want an error", in)
		}
	}
}
