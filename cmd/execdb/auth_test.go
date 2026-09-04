package main

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestResolveAuthNoOpWhenUserUnset(t *testing.T) {
	opts := &options{}
	if err := resolveAuthWith(opts, failPrompt(t)); err != nil {
		t.Fatal(err)
	}
	if opts.password != "" {
		t.Errorf("password = %q, want empty (Zero-Auth)", opts.password)
	}
}

func TestResolveAuthPrefersEnvVar(t *testing.T) {
	t.Setenv("EXECDB_PASSWORD", "from-env")
	opts := &options{user: "alice"}
	if err := resolveAuthWith(opts, failPrompt(t)); err != nil {
		t.Fatal(err)
	}
	if opts.password != "from-env" {
		t.Errorf("password = %q, want %q", opts.password, "from-env")
	}
}

func TestResolveAuthEmptyEnvVarIsRespectedIfExplicitlySet(t *testing.T) {
	t.Setenv("EXECDB_PASSWORD", "")
	opts := &options{user: "alice"}
	if err := resolveAuthWith(opts, failPrompt(t)); err != nil {
		t.Fatal(err)
	}
	if opts.password != "" {
		t.Errorf("password = %q, want empty string", opts.password)
	}
}

func TestResolveAuthServerModeWithoutEnvVarErrors(t *testing.T) {
	os.Unsetenv("EXECDB_PASSWORD")
	t.Cleanup(func() { os.Unsetenv("EXECDB_PASSWORD") })
	opts := &options{user: "alice", noRepl: true}
	if err := resolveAuthWith(opts, failPrompt(t)); err == nil {
		t.Error("expected an error when --no-repl has no EXECDB_PASSWORD and no terminal to prompt")
	}
}

func TestResolveAuthREPLModePromptsWhenEnvVarUnset(t *testing.T) {
	os.Unsetenv("EXECDB_PASSWORD")
	t.Cleanup(func() { os.Unsetenv("EXECDB_PASSWORD") })
	opts := &options{user: "alice"}
	called := false
	prompt := func() (string, error) {
		called = true
		return "typed-password", nil
	}
	if err := resolveAuthWith(opts, prompt); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected the interactive prompt to be invoked")
	}
	if opts.password != "typed-password" {
		t.Errorf("password = %q, want %q", opts.password, "typed-password")
	}
}

func TestResolveAuthPromptErrorPropagates(t *testing.T) {
	os.Unsetenv("EXECDB_PASSWORD")
	t.Cleanup(func() { os.Unsetenv("EXECDB_PASSWORD") })
	opts := &options{user: "alice"}
	wantErr := errors.New("boom")
	prompt := func() (string, error) { return "", wantErr }
	if err := resolveAuthWith(opts, prompt); err == nil {
		t.Error("expected the prompt's error to propagate")
	}
}

func failPrompt(t *testing.T) func() (string, error) {
	return func() (string, error) {
		t.Helper()
		t.Fatal("prompt should not have been called")
		return "", nil
	}
}

func TestAuthenticateConnectionZeroAuth(t *testing.T) {
	var buf bytes.Buffer
	if !authenticateConnection(&buf, "", "", nil) {
		t.Fatal("expected Zero-Auth (user == \"\") to succeed")
	}
	if buf.Bytes()[0] != 'R' {
		t.Fatalf("expected an Authentication message, got %q", buf.Bytes()[0])
	}
}

func TestAuthenticateConnectionCorrectPassword(t *testing.T) {
	var buf bytes.Buffer
	writeMessage(&buf, 'p', func(b *msgBuilder) { b.cstring("secret") })
	startupParams := map[string]string{"user": "alice"}

	if !authenticateConnection(&buf, "alice", "secret", startupParams) {
		t.Fatal("expected authentication to succeed")
	}
	// The remaining bytes are what authenticateConnection wrote: the
	// AuthenticationCleartextPassword challenge, then AuthenticationOk.
	if !bytes.Contains(buf.Bytes(), []byte{'R', 0, 0, 0, 8, 0, 0, 0, 3}) {
		t.Error("expected AuthenticationCleartextPassword in the response")
	}
	if !bytes.Contains(buf.Bytes(), []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0}) {
		t.Error("expected AuthenticationOk in the response")
	}
}

func TestAuthenticateConnectionWrongPassword(t *testing.T) {
	var buf bytes.Buffer
	writeMessage(&buf, 'p', func(b *msgBuilder) { b.cstring("wrong") })
	startupParams := map[string]string{"user": "alice"}

	if authenticateConnection(&buf, "alice", "secret", startupParams) {
		t.Fatal("expected authentication to fail")
	}
	if !bytes.Contains(buf.Bytes(), []byte("28P01")) {
		t.Errorf("expected SQLSTATE 28P01 in the response, got %q", buf.Bytes())
	}
}

func TestAuthenticateConnectionWrongUsername(t *testing.T) {
	var buf bytes.Buffer
	writeMessage(&buf, 'p', func(b *msgBuilder) { b.cstring("secret") })
	startupParams := map[string]string{"user": "mallory"} // StartupMessage's user != --user

	if authenticateConnection(&buf, "alice", "secret", startupParams) {
		t.Fatal("expected authentication to fail on a StartupMessage user mismatch")
	}
	if !bytes.Contains(buf.Bytes(), []byte("28P01")) {
		t.Errorf("expected SQLSTATE 28P01 in the response, got %q", buf.Bytes())
	}
}

func TestAuthenticateConnectionRejectsNonPasswordMessage(t *testing.T) {
	var buf bytes.Buffer
	writeMessage(&buf, 'Q', func(b *msgBuilder) { b.cstring("SELECT 1") }) // wrong message type
	startupParams := map[string]string{"user": "alice"}

	if authenticateConnection(&buf, "alice", "secret", startupParams) {
		t.Fatal("expected authentication to fail on an unexpected message type")
	}
}
