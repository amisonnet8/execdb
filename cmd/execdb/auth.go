package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// resolveAuth fills in opts.password when --user is set, following spec
// §8's priority order: the EXECDB_PASSWORD environment variable first (in
// either mode, no prompt), then an interactive masked prompt in REPL mode,
// then an error in server mode (--no-repl has no terminal to prompt).
// opts.password stays "" (and is never used) when --user is unset --
// Zero-Auth, the default.
func resolveAuth(opts *options) error {
	return resolveAuthWith(opts, promptPassword)
}

// resolveAuthWith is resolveAuth's implementation, taking the interactive
// prompt as a parameter so auth_test.go can exercise every branch of the
// priority order without a real terminal (the same dependency-injection
// pattern interrupt.go's exitFunc uses for testability).
func resolveAuthWith(opts *options, prompt func() (string, error)) error {
	if opts.user == "" {
		return nil
	}
	if pw, ok := os.LookupEnv("EXECDB_PASSWORD"); ok {
		opts.password = pw
		return nil
	}
	if opts.noRepl {
		return fmt.Errorf("--user requires EXECDB_PASSWORD to be set in server mode (--no-repl has no terminal to prompt)")
	}
	pw, err := prompt()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	opts.password = pw
	return nil
}

// promptPassword reads a password from the controlling terminal with
// input masked (spec §8's "Password: " prompt), via golang.org/x/term's
// ReadPassword.
func promptPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Password: ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(pw), nil
}

// authenticateConnection performs spec §8's optional cleartext password
// challenge for one pgwire connection. If user is "" (the default,
// --user unset), Zero-Auth applies and this just sends AuthenticationOk.
// Otherwise it challenges the client with AuthenticationCleartextPassword,
// reads the PasswordMessage response, and checks both the StartupMessage's
// own "user" startup parameter and the submitted password against the
// configured values -- rejecting with SQLSTATE 28P01 (invalid_password,
// real PostgreSQL's own code for this case) on any mismatch. It reports
// whether the connection is still usable; on false the caller should just
// give up on it (an ErrorResponse has already been sent where applicable,
// matching real PostgreSQL closing the connection after a failed
// authentication attempt).
func authenticateConnection(rw io.ReadWriter, user, password string, startupParams map[string]string) bool {
	if user == "" {
		return writeAuthenticationOk(rw) == nil
	}

	if err := writeAuthenticationCleartextPassword(rw); err != nil {
		return false
	}
	msg, err := readFrontendMessage(rw)
	if err != nil || msg.Type != 'p' {
		return false
	}
	submitted := cStringFromBody(msg.Body)

	if startupParams["user"] != user || submitted != password {
		writeErrorResponse(rw, sqlstateInvalidPassword,
			fmt.Sprintf("password authentication failed for user %q", startupParams["user"]))
		return false
	}
	return writeAuthenticationOk(rw) == nil
}
