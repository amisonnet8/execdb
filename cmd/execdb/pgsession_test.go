package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestHandleSessionCommandOnlySetAndShow(t *testing.T) {
	params := newSessionParams(nil)
	var buf bytes.Buffer
	if handled, _ := handleSessionCommand(&buf, params, "SELECT", "SELECT 1"); handled {
		t.Error("SELECT must not be intercepted as a session command")
	}
	if handled, _ := handleSessionCommand(&buf, params, "INSERT", "INSERT INTO t VALUES (1)"); handled {
		t.Error("INSERT must not be intercepted as a session command")
	}
}

func TestHandleSetStoresValueAndRespondsSET(t *testing.T) {
	params := newSessionParams(nil)
	var buf bytes.Buffer
	handled, err := handleSessionCommand(&buf, params, "SET", "SET extra_float_digits = 3")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if params["extra_float_digits"] != "3" {
		t.Errorf("params[extra_float_digits] = %q, want %q", params["extra_float_digits"], "3")
	}
	if buf.Bytes()[0] != 'C' {
		t.Fatalf("expected CommandComplete ('C'), got %q", buf.Bytes()[0])
	}
	if !bytes.Contains(buf.Bytes(), []byte("SET\x00")) {
		t.Errorf("expected command tag \"SET\", got %q", buf.Bytes())
	}
}

func TestHandleSetVariants(t *testing.T) {
	cases := []struct {
		stmt      string
		wantName  string
		wantValue string
	}{
		{"SET client_encoding TO 'UTF8'", "client_encoding", "UTF8"},
		{"set TimeZone = 'UTC'", "timezone", "UTC"},
		{"SET SESSION extra_float_digits = 3", "extra_float_digits", "3"},
		{"SET LOCAL statement_timeout TO 0", "statement_timeout", "0"},
		{"SET x = 'it''s'", "x", "it's"},
	}
	for _, c := range cases {
		params := newSessionParams(nil)
		var buf bytes.Buffer
		handled, err := handleSessionCommand(&buf, params, "SET", c.stmt)
		if !handled || err != nil {
			t.Fatalf("%q: handled=%v err=%v", c.stmt, handled, err)
		}
		if got := params[c.wantName]; got != c.wantValue {
			t.Errorf("%q: params[%q] = %q, want %q", c.stmt, c.wantName, got, c.wantValue)
		}
	}
}

func TestHandleSetUnsupportedSyntaxErrors(t *testing.T) {
	params := newSessionParams(nil)
	var buf bytes.Buffer
	handled, err := handleSessionCommand(&buf, params, "SET", "SET")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if buf.Bytes()[0] != 'E' {
		t.Fatalf("expected ErrorResponse ('E') for malformed SET, got %q", buf.Bytes()[0])
	}
}

func TestHandleShowSeededFromStartupParams(t *testing.T) {
	seed := [][2]string{{"TimeZone", "UTC"}}
	params := newSessionParams(seed)
	var buf bytes.Buffer
	handled, err := handleSessionCommand(&buf, params, "SHOW", "SHOW TimeZone")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if buf.Bytes()[0] != 'T' {
		t.Fatalf("expected RowDescription ('T') first, got %q", buf.Bytes()[0])
	}
	if !bytes.Contains(buf.Bytes(), []byte("UTC")) {
		t.Errorf("expected the seeded value %q in the response, got %q", "UTC", buf.Bytes())
	}
}

func TestHandleShowReflectsPriorSet(t *testing.T) {
	params := newSessionParams(nil)
	var buf bytes.Buffer
	if err := handleSet(&buf, params, "SET application_name = 'myapp'"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	handled, err := handleSessionCommand(&buf, params, "SHOW", "SHOW application_name")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("myapp")) {
		t.Errorf("expected SHOW to reflect the prior SET, got %q", buf.Bytes())
	}
}

func TestHandleShowUnknownParamReturnsEmptyString(t *testing.T) {
	params := newSessionParams(nil)
	var buf bytes.Buffer
	handled, err := handleSessionCommand(&buf, params, "SHOW", "SHOW nonexistent_param")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	body := buf.Bytes()
	// A DataRow ('D') must follow the RowDescription with a zero-length
	// (not NULL, i.e. not -1-length) field: "unset" is an empty string,
	// distinct from SQL NULL.
	idx := bytes.IndexByte(body, 'D')
	if idx < 0 {
		t.Fatalf("no DataRow found in %q", body)
	}
	fieldLen := int32(binary.BigEndian.Uint32(body[idx+7 : idx+11]))
	if fieldLen != 0 {
		t.Errorf("field length = %d, want 0 (empty string, not NULL)", fieldLen)
	}
}

func TestHandleShowUnsupportedSyntaxErrors(t *testing.T) {
	params := newSessionParams(nil)
	var buf bytes.Buffer
	handled, err := handleSessionCommand(&buf, params, "SHOW", "SHOW ALL")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if buf.Bytes()[0] != 'E' {
		t.Fatalf("expected ErrorResponse ('E') for unsupported SHOW ALL, got %q", buf.Bytes()[0])
	}
}

func TestUnquoteSetValue(t *testing.T) {
	cases := map[string]string{
		"'utf8'":  "utf8",
		"on":      "on",
		"3":       "3",
		"'it''s'": "it's",
		"":        "",
	}
	for in, want := range cases {
		if got := unquoteSetValue(in); got != want {
			t.Errorf("unquoteSetValue(%q) = %q, want %q", in, got, want)
		}
	}
}
