// Command pgclient is a small pgx-based connectivity check for ExecDB's
// PostgreSQL wire protocol implementation (spec §8). It exists to satisfy
// the phase 1 requirement of testing with a Go driver in addition to
// psql (PLAN.md). It lives under examples/ specifically so pgx
// (github.com/jackc/pgx/v5) never becomes a dependency of cmd/execdb's
// shipped binary (.claude/rules/binary-size.md): go.mod lists it, but
// nothing in cmd/execdb imports it, so it is never linked into
// bin/execdb.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pgclient <connString>")
		fmt.Fprintln(os.Stderr, `  e.g. pgclient "postgres://any@127.0.0.1:15432/any?sslmode=disable&default_query_exec_mode=simple_protocol"`)
		fmt.Fprintln(os.Stderr, "  default_query_exec_mode=simple_protocol is required: pgx defaults to the")
		fmt.Fprintln(os.Stderr, "  extended query protocol, which ExecDB does not implement in phase 1")
		fmt.Fprintln(os.Stderr, "  (.claude/rules/pgwire.md).")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func run(connString string) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	// Scan into *string, not *int: phase 1 reports every column as OID 25
	// (text) regardless of its actual SQLite affinity
	// (.claude/rules/pgwire.md's "決め打ちで実装せず実接続で確定" applies
	// to the real type mapping, deferred to phase 4). pgx enforces this
	// strictly -- unlike psql's text-rendering CLI, it refuses to scan a
	// column declared as text into *int at all.
	var one string
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("SELECT 1: %w", err)
	}
	if one != "1" {
		return fmt.Errorf("SELECT 1 returned %q, want %q", one, "1")
	}

	var s string
	if err := conn.QueryRow(ctx, "SELECT 'hello'").Scan(&s); err != nil {
		return fmt.Errorf("SELECT 'hello': %w", err)
	}
	if s != "hello" {
		return fmt.Errorf("SELECT 'hello' returned %q, want %q", s, "hello")
	}

	var null *string
	if err := conn.QueryRow(ctx, "SELECT NULL").Scan(&null); err != nil {
		return fmt.Errorf("SELECT NULL: %w", err)
	}
	if null != nil {
		return fmt.Errorf("SELECT NULL returned %q, want nil", *null)
	}

	// spec §2: DDL must be rejected via the external I/F, even from a real
	// driver -- and pgx must actually parse our ErrorResponse into a
	// structured error with the right SQLSTATE (spec §8: 42501), not just
	// fail some other way.
	_, err = conn.Exec(ctx, "CREATE TABLE pgclient_should_not_exist(a INTEGER)")
	if err == nil {
		return errors.New("expected CREATE TABLE to be rejected via the external I/F, but it succeeded")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("expected a structured PgError for the rejected CREATE TABLE, got: %w", err)
	}
	if pgErr.Code != "42501" {
		return fmt.Errorf("expected SQLSTATE 42501 for the rejected CREATE TABLE, got %q (%s)", pgErr.Code, pgErr.Message)
	}

	return nil
}
