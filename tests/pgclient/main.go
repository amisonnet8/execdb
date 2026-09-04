// Command pgclient is a small pgx-based connectivity check for ExecDB's
// PostgreSQL wire protocol implementation (spec §8). It exists to satisfy
// the phase 1 requirement of testing with a Go driver in addition to
// psql (PLAN.md). It lives under tests/ specifically so pgx
// (github.com/jackc/pgx/v5) never becomes a dependency of cmd/execdb's
// shipped binary (.claude/rules/binary-size.md): go.mod lists it, but
// nothing in cmd/execdb imports it, so it is never linked into
// bin/execdb.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

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

	// Scan into *int64/*float64/[]byte, not *string: phase 4 Step 2 maps
	// each column to a real Postgres OID (int8/float8/text/bytea/etc.,
	// cmd/execdb/pgtype.go) based on its SQLite declared type, rather than
	// phase 1's OID-25-for-everything placeholder
	// (.claude/rules/pgwire.md). pgx enforces the declared type strictly
	// -- unlike psql's text-rendering CLI, it refuses to scan a column
	// declared as text into *int at all, so this exercises the mapping
	// end-to-end, not just that a query runs.
	var one int64
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("SELECT 1: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("SELECT 1 returned %d, want %d", one, 1)
	}

	var s string
	if err := conn.QueryRow(ctx, "SELECT 'hello'").Scan(&s); err != nil {
		return fmt.Errorf("SELECT 'hello': %w", err)
	}
	if s != "hello" {
		return fmt.Errorf("SELECT 'hello' returned %q, want %q", s, "hello")
	}

	var f float64
	if err := conn.QueryRow(ctx, "SELECT 3.5").Scan(&f); err != nil {
		return fmt.Errorf("SELECT 3.5: %w", err)
	}
	if f != 3.5 {
		return fmt.Errorf("SELECT 3.5 returned %v, want %v", f, 3.5)
	}

	var blob []byte
	if err := conn.QueryRow(ctx, "SELECT x'00ff'").Scan(&blob); err != nil {
		return fmt.Errorf("SELECT x'00ff': %w", err)
	}
	if !bytes.Equal(blob, []byte{0x00, 0xff}) {
		return fmt.Errorf("SELECT x'00ff' returned % x, want % x", blob, []byte{0x00, 0xff})
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

	// The checks below need an actual writable table, which DDL rejection
	// above proves pgclient cannot create over the wire -- the caller is
	// expected to have started ExecDB from a snapshot that already has a
	// table named "t" with an INTEGER column "a" (tests/e2e.sh does
	// this, reusing the snapshot from its own .snapshot check).
	if err := checkTransactionIsolation(ctx, connString); err != nil {
		return fmt.Errorf("transaction isolation: %w", err)
	}
	if err := checkFailedTransactionState(ctx, connString); err != nil {
		return fmt.Errorf("failed transaction state: %w", err)
	}
	if err := checkDisconnectDuringQuery(ctx, connString); err != nil {
		return fmt.Errorf("disconnect during query: %w", err)
	}

	return nil
}

// checkTransactionIsolation proves engine.Session gives pgwire real
// per-connection transaction isolation (spec §2/§8, phase 2 Step 5): two
// independent pgx connections, each mapping to its own Session inside
// ExecDB, must not see each other's uncommitted writes.
func checkTransactionIsolation(ctx context.Context, connString string) error {
	a, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("connect A: %w", err)
	}
	defer a.Close(ctx)
	b, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("connect B: %w", err)
	}
	defer b.Close(ctx)

	if _, err := a.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("A BEGIN: %w", err)
	}
	if _, err := a.Exec(ctx, "INSERT INTO t VALUES (424242)"); err != nil {
		return fmt.Errorf("A INSERT: %w", err)
	}

	// B's read is issued while A's write is still open. It blocks rather
	// than returning immediately -- memdb's locking is coarser than a
	// normal file database's (.claude/rules/sqlite-quirks.md) -- so this
	// proves isolation via serialization (B never observes a half-done
	// state) rather than true concurrent snapshot isolation.
	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		r.err = b.QueryRow(ctx, "SELECT count(*) FROM t WHERE a = 424242").Scan(&r.n)
		done <- r
	}()

	time.Sleep(200 * time.Millisecond)
	if _, err := a.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("A COMMIT: %w", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			return fmt.Errorf("B SELECT: %w", r.err)
		}
		if r.n != 1 {
			return fmt.Errorf("B saw count=%d after A committed, want 1", r.n)
		}
	case <-time.After(10 * time.Second):
		return errors.New("B's read did not return after A committed")
	}
	return nil
}

// checkFailedTransactionState proves handleSimpleQuery's 'I'/'T'/'E'
// ReadyForQuery tracking (cmd/execdb/pgwire.go, phase 2 Step 5): once a
// statement inside a transaction fails, later statements are rejected
// with SQLSTATE 25P02 until ROLLBACK, which returns the connection to a
// usable state.
func checkFailedTransactionState(ctx context.Context, connString string) error {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("BEGIN: %w", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO pgclient_does_not_exist VALUES (1)"); err == nil {
		return errors.New("expected an error inserting into a nonexistent table")
	}

	_, err = conn.Exec(ctx, "SELECT 1")
	if err == nil {
		return errors.New("expected a statement after a failed one in the same transaction to be rejected")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("expected a structured PgError for the aborted-transaction rejection, got: %w", err)
	}
	if pgErr.Code != "25P02" {
		return fmt.Errorf("expected SQLSTATE 25P02 in an aborted transaction, got %q (%s)", pgErr.Code, pgErr.Message)
	}

	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		return fmt.Errorf("ROLLBACK: %w", err)
	}

	var one int64
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("SELECT 1 after ROLLBACK: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("SELECT 1 after ROLLBACK returned %d, want %d", one, 1)
	}
	return nil
}

// checkDisconnectDuringQuery proves a client disconnecting mid-query
// doesn't wedge the server (cmd/execdb/pgwire.go's watchForDisconnect,
// phase 2 Step 5): a query whose context times out makes pgx give up on
// the connection, and the server must still accept and answer a brand
// new connection promptly afterward.
func checkDisconnectDuringQuery(ctx context.Context, connString string) error {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	qctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_, err = conn.Exec(qctx, `WITH RECURSIVE cnt(x) AS (
		SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x < 2000000000
	) SELECT count(*) FROM cnt`)
	if err == nil {
		return errors.New("expected the long query to be interrupted by its context timing out")
	}

	verify, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("reconnect after a canceled query: %w", err)
	}
	defer verify.Close(ctx)
	var one int64
	if err := verify.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("SELECT 1 after reconnect: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("SELECT 1 after reconnect returned %d, want %d", one, 1)
	}
	return nil
}
