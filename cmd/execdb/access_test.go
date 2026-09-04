package main

import "testing"

func TestCheckExternalAccessAllowsDMLAndTCL(t *testing.T) {
	allowed := []string{
		"SELECT 1",
		"  select * from t",
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET a = 1",
		"DELETE FROM t",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"EXPLAIN SELECT 1",
	}
	for _, stmt := range allowed {
		if err := checkExternalAccess(stmt); err != nil {
			t.Errorf("checkExternalAccess(%q) = %v, want nil", stmt, err)
		}
	}
}

func TestCheckExternalAccessRejectsDDL(t *testing.T) {
	rejected := []string{
		"CREATE TABLE t(a)",
		"DROP TABLE t",
		"ALTER TABLE t ADD COLUMN b",
		"TRUNCATE TABLE t",
	}
	for _, stmt := range rejected {
		err := checkExternalAccess(stmt)
		if err == nil {
			t.Errorf("checkExternalAccess(%q) = nil, want an error", stmt)
			continue
		}
		if err.Error() != "DDL statements are not allowed via external interface" {
			t.Errorf("checkExternalAccess(%q) error = %q, want the spec §8 DDL wording", stmt, err.Error())
		}
	}
}

func TestCheckExternalAccessRejectsOtherCategory(t *testing.T) {
	// spec §2's "その他（外部I/Fでは常に拒否）" category, added in Step 4:
	// ATTACH DATABASE '/etc/passwd' style attacks are the reason this
	// category exists under Zero-Auth.
	rejected := []string{
		"ATTACH DATABASE '/etc/passwd' AS x",
		"DETACH DATABASE x",
		"PRAGMA journal_mode=WAL",
		"VACUUM",
		"REINDEX t",
	}
	for _, stmt := range rejected {
		if err := checkExternalAccess(stmt); err == nil {
			t.Errorf("checkExternalAccess(%q) = nil, want an error", stmt)
		}
	}
}

func TestCheckExternalAccessCatchesMultiStatementBypass(t *testing.T) {
	// The exact attack this exists to stop: hiding a DROP behind an
	// allowed first statement in one Simple Query message.
	if err := checkExternalAccess("SELECT 1; DROP TABLE t"); err == nil {
		t.Error("expected the DROP hidden after a SELECT to be rejected")
	}
	if err := checkExternalAccess("SELECT 1; SELECT 2"); err != nil {
		t.Errorf("two allowed statements should not be rejected: %v", err)
	}
}

func TestCheckExternalAccessIgnoresSemicolonsInStringsAndComments(t *testing.T) {
	if err := checkExternalAccess("SELECT ';DROP TABLE t;' AS x"); err != nil {
		t.Errorf("a semicolon inside a string literal must not split statements: %v", err)
	}
	if err := checkExternalAccess("SELECT 1 -- ; DROP TABLE t\n"); err != nil {
		t.Errorf("a semicolon inside a line comment must not split statements: %v", err)
	}
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"SELECT 1", []string{"SELECT 1"}},
		{"SELECT 1;", []string{"SELECT 1"}},
		{"SELECT 1; SELECT 2", []string{"SELECT 1", " SELECT 2"}},
		{"SELECT ';';", []string{"SELECT ';'"}},
		{"", nil},
		{"   ", nil},
	}
	for _, c := range cases {
		got := splitStatements(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitStatements(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitStatements(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestFirstKeywordSkipsLeadingComments(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                 "SELECT",
		"  \n\t select 1":          "SELECT",
		"-- a comment\nDROP TABLE": "DROP",
		"/* c */ CREATE TABLE t":   "CREATE",
	}
	for in, want := range cases {
		if got := firstKeyword(in); got != want {
			t.Errorf("firstKeyword(%q) = %q, want %q", in, got, want)
		}
	}
}
