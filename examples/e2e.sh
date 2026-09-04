#!/usr/bin/env bash
# examples/e2e.sh -- end-to-end checks for `make test` (.claude/rules/testing.md).
#
# Exercises the REPL, .snapshot/.load/.overwrite, pgwire (TCP+UDS), a real
# Go driver (examples/pgclient, pgx), and `go install`, against the
# binary at bin/execdb (built by `make build`, which this script assumes
# has already run -- see the "e2e" Makefile target).
#
# All destructive operations (.overwrite in particular) run against a
# COPY of bin/execdb in a scratch directory, never the build artifact
# itself.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/execdb"
WORK="$(mktemp -d)"

# SERVER_PIDS is the safety net for background servers this script starts:
# if a check fails midway (set -e / fail() below) and a stop_server call
# is skipped as a result, cleanup() below still kills them, instead of
# leaking a process that holds a port open for every later run.
SERVER_PIDS=()

cleanup() {
  local status=$?
  for pid in "${SERVER_PIDS[@]:-}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  rm -rf "$WORK"
  exit "$status"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok - $*"; }

[ -x "$BIN" ] || fail "bin/execdb not found or not executable; run 'make build' first"
command -v psql >/dev/null 2>&1 || fail "psql not found (install postgresql-client)"

# start_server launches "$@" in the background with its CWD set to $WORK
# (so server-mode's auto-save-on-shutdown, which writes a relative
# filename, lands in the scratch directory instead of wherever this
# script happened to be run from), records its PID for cleanup()'s safety
# net, and prints the PID. "exec" inside the subshell replaces the
# subshell's own process image with the server, so $! is the server's own
# PID, not a wrapper's.
start_server() {
  (cd "$WORK" && exec "$@") >/dev/null 2>&1 &
  local pid=$!
  SERVER_PIDS+=("$pid")
  echo "$pid"
}

# stop_server sends SIGTERM (triggering server-mode auto-save,
# .claude/rules/cli-output.md) and polls until the process is gone. It
# deliberately does not use the "wait" builtin: start_server's PID is
# captured through a "pid=$(start_server ...)" command substitution, so
# by the time this runs, the server is no longer a direct child of this
# shell ("wait" refuses to wait for a PID that isn't one), and kill -0
# works on any visible PID regardless of parentage.
stop_server() {
  local pid="$1" tries=100
  kill "$pid" >/dev/null 2>&1 || true
  while kill -0 "$pid" 2>/dev/null; do
    tries=$((tries - 1))
    [ "$tries" -gt 0 ] || break
    sleep 0.1
  done
}

wait_for_tcp() {
  local host="$1" port="$2" tries=100
  while ! (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; do
    tries=$((tries - 1))
    [ "$tries" -gt 0 ] || fail "timed out waiting for $host:$port to accept connections"
    sleep 0.1
  done
  exec 3>&- 3<&- 2>/dev/null || true
}

wait_for_socket() {
  local path="$1" tries=100
  while [ ! -S "$path" ]; do
    tries=$((tries - 1))
    [ "$tries" -gt 0 ] || fail "timed out waiting for socket $path"
    sleep 0.1
  done
}

# --- REPL: CREATE/INSERT/.tables/.schema/SELECT (spec §3) ---
cp "$BIN" "$WORK/repl1"
out="$(printf 'CREATE TABLE t(a INTEGER, b TEXT);\nINSERT INTO t VALUES (1, %s);\n.tables\nSELECT * FROM t;\n.exit\n' "'x'" | "$WORK/repl1")"
echo "$out" | grep -qx 't' || fail ".tables did not list table t"
echo "$out" | grep -qx '1|x' || fail "SELECT did not return the inserted row"
pass "REPL: CREATE/INSERT/.tables/SELECT"

# --- .snapshot produces a standalone runnable file (spec §4, §7) ---
printf 'CREATE TABLE t(a INTEGER);\nINSERT INTO t VALUES (7);\n.snapshot %s/snap1\n.exit\n' "$WORK" | "$WORK/repl1" >/dev/null
[ -f "$WORK/snap1" ] || fail ".snapshot did not create a file"
chmod +x "$WORK/snap1"
out="$(printf 'SELECT * FROM t;\n.exit\n' | "$WORK/snap1")"
echo "$out" | grep -qx '7' || fail "the snapshot file does not contain the seeded row"
pass ".snapshot: produces a standalone runnable file"

# --- .load imports another file's data, replacing (not merging) (spec §4) ---
cp "$BIN" "$WORK/loader"
out="$(printf '.load %s/snap1\nSELECT * FROM t;\n.exit\n' "$WORK" | "$WORK/loader")"
echo "$out" | grep -qx '7' || fail ".load did not import the row from snap1"
pass ".load: imports data from another ExecDB file"

# --- .dump renders schema+data as SQL text, replayable by piping it into
# a fresh REPL process (spec §3, §5 use case 2: sharing a data state).
# The seeded schema includes a TRIGGER: replaying a dumped CREATE TRIGGER
# through the REPL's own line-based statement reader only works because
# of completeStatements (complete.go, phase 3 Step 4), which defers to
# SQLite's own sqlite3_complete() to know a trigger body's internal ";"
# isn't a statement boundary -- without it, this exact check would fail
# with "SQL logic error: incomplete input". ---
cp "$BIN" "$WORK/dumpsrc"
dump_out="$(printf 'CREATE TABLE t(a INTEGER PRIMARY KEY AUTOINCREMENT, b TEXT);\nINSERT INTO t(b) VALUES (%s);\nINSERT INTO t(b) VALUES (NULL);\nCREATE INDEX idx_t_b ON t(b);\nCREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END;\n.dump\n.exit\n' "'it''s'" | "$WORK/dumpsrc")"
cp "$BIN" "$WORK/dumptarget"
out="$(printf '%s\nSELECT a, b FROM t ORDER BY a;\n.exit\n' "$dump_out" | "$WORK/dumptarget")"
echo "$out" | grep -qx "1|it's" || fail ".dump output did not replay the first row correctly (got: $out)"
echo "$out" | grep -qx '2|' || fail ".dump output did not replay the NULL row correctly (got: $out)"
echo "$dump_out" | grep -q 'CREATE TRIGGER trg' || fail ".dump output did not include the trigger"
pass ".dump: schema (including a TRIGGER), index, and data replay correctly when piped into a fresh REPL"

# --- completeStatements: a CREATE TRIGGER typed directly into the REPL
# (not via .dump) across several lines, with a CASE...END expression
# inside the trigger body -- the case a naive BEGIN/END depth counter
# cannot handle safely, since CASE's END has no matching BEGIN of its
# own (spec §3, PLAN.md phase 3 Step 4) ---
cp "$BIN" "$WORK/trigsrc"
out="$(printf 'CREATE TABLE t(a);\nCREATE TRIGGER trg AFTER INSERT ON t BEGIN\nSELECT CASE WHEN NEW.a > 0 THEN 1 ELSE 0 END;\nEND;\nINSERT INTO t VALUES (5);\nSELECT * FROM t;\n.exit\n' | "$WORK/trigsrc")"
echo "$out" | grep -q 'incomplete input' && fail "a multi-line trigger with an internal CASE...END was not accepted by the REPL"
echo "$out" | grep -qx '5' || fail "INSERT after the trigger did not take effect (got: $out)"
pass "REPL: a multi-line CREATE TRIGGER with an internal CASE...END is accepted as one statement"

# --- .import bulk-loads a CSV file, creating the table from its header
# row when one doesn't already exist (spec §3, §5 use case 1: seed data) ---
cp "$BIN" "$WORK/importer"
printf 'a,b\n1,hello\n2,"quoted, value"\n' >"$WORK/seed.csv"
out="$(printf '.import %s/seed.csv t\nSELECT a, b FROM t ORDER BY a;\n.exit\n' "$WORK" | "$WORK/importer")"
echo "$out" | grep -qx '1|hello' || fail ".import row 1 did not come through correctly (got: $out)"
echo "$out" | grep -qx '2|quoted, value' || fail ".import did not preserve a quoted comma-containing field (got: $out)"
pass ".import: CSV data loads into a newly created table"

# --- .overwrite persists into the running executable (spec §4, §7) ---
cp "$BIN" "$WORK/ow"
before_size=$(wc -c <"$WORK/ow")
printf 'CREATE TABLE t(a INTEGER);\nINSERT INTO t VALUES (99);\n.overwrite\n' | "$WORK/ow" >/dev/null
after_size=$(wc -c <"$WORK/ow")
[ "$after_size" -gt "$before_size" ] || fail ".overwrite did not grow the file as expected ($before_size -> $after_size)"
[ ! -e "$WORK/ow.execdb_old" ] || fail ".execdb_old sidecar was not cleaned up after .overwrite"
out="$(printf 'SELECT * FROM t;\n.exit\n' | "$WORK/ow")"
echo "$out" | grep -qx '99' || fail "data did not survive .overwrite"
pass ".overwrite: persists data into the running executable and cleans up its sidecar"

# --- pgwire over TCP: psql SELECT, DDL rejected, multi-statement bypass rejected (spec §8) ---
# Starts from snap1 (already has table t(a INTEGER), seeded above) rather
# than a blank binary: DDL is rejected via the external I/F (spec §2), so
# examples/pgclient's transaction-isolation/failed-tx-state checks below
# need a table that already exists.
cp "$WORK/snap1" "$WORK/pgtcp"
chmod +x "$WORK/pgtcp"
pid=$(start_server "$WORK/pgtcp" -n -p 127.0.0.1:15532 -q)
wait_for_tcp 127.0.0.1 15532

out="$(PGCONNECT_TIMEOUT=5 psql -h 127.0.0.1 -p 15532 -U any -d any -tAc 'SELECT 1;')"
[ "$out" = "1" ] || fail "psql TCP SELECT 1 returned $out"
pass "pgwire TCP: psql SELECT 1"

ddl_out="$(psql -h 127.0.0.1 -p 15532 -U any -d any -c 'CREATE TABLE t(a INTEGER);' 2>&1 || true)"
echo "$ddl_out" | grep -q 'not allowed via external interface' \
  || fail "pgwire TCP: CREATE TABLE was not rejected (got: $ddl_out)"
pass "pgwire TCP: DDL rejected"

attach_out="$(psql -h 127.0.0.1 -p 15532 -U any -d any -c "ATTACH DATABASE '/etc/passwd' AS x;" 2>&1 || true)"
echo "$attach_out" | grep -q 'not allowed via external interface' \
  || fail "pgwire TCP: ATTACH was not rejected (got: $attach_out)"
pass "pgwire TCP: ATTACH rejected"

bypass_out="$(psql -h 127.0.0.1 -p 15532 -U any -d any -c 'SELECT 1; DROP TABLE nonexistent;' 2>&1 || true)"
echo "$bypass_out" | grep -q 'not allowed via external interface' \
  || fail "pgwire TCP: the DROP hidden after a SELECT was not rejected (got: $bypass_out)"
pass "pgwire TCP: multi-statement DDL bypass rejected"

# --- examples/pgclient (pgx v5): a second, independent driver implementation ---
# default_query_exec_mode=simple_protocol is required: pgx defaults to the
# extended query protocol, which ExecDB does not implement in phase 1
# (.claude/rules/pgwire.md).
go run "$ROOT/examples/pgclient" "postgres://any@127.0.0.1:15532/any?sslmode=disable&default_query_exec_mode=simple_protocol" \
  || fail "examples/pgclient (pgx) checks failed"
pass "pgwire TCP: examples/pgclient (pgx) SELECT/NULL/DDL-rejection checks"

stop_server "$pid"

# --- a pgwire session held open across a concurrent REPL .load sees the
#     newly loaded data (spec §2/§4; phase 2 Step 2's in-place backup and
#     Step 5's 1-connection-per-Session wiring) ---
cp "$BIN" "$WORK/pgsession"
FIFO="$WORK/repl_in"
mkfifo "$FIFO"
"$WORK/pgsession" -p 127.0.0.1:15534 -q <"$FIFO" >/dev/null 2>&1 &
repl_pid=$!
SERVER_PIDS+=("$repl_pid")
exec 9>"$FIFO"
wait_for_tcp 127.0.0.1 15534

echo "CREATE TABLE u(b TEXT);" >&9
echo "INSERT INTO u VALUES ('before-load');" >&9
sleep 0.2

# Fires .load into the REPL while the single psql session below (opened
# once, held open across both SELECTs via "\! sleep") is mid-script, so
# the .load genuinely runs concurrently with an open pgwire session.
( sleep 0.3; echo ".load $WORK/snap1" >&9 ) &
loader_bg=$!

session_out="$(psql -h 127.0.0.1 -p 15534 -U any -d any -tA <<'SQL'
SELECT b FROM u;
\! sleep 1
SELECT a FROM t;
SQL
)"
wait "$loader_bg"

echo "$session_out" | grep -qx 'before-load' \
  || fail "pgwire session did not see REPL-inserted data before .load (got: $session_out)"
echo "$session_out" | grep -qx '7' \
  || fail "pgwire session did not see .load's new data while held open (got: $session_out)"
pass "pgwire session survives a concurrent REPL .load and sees the new data"

echo ".exit" >&9
exec 9>&-
stop_server "$repl_pid"

# --- pgwire over UNIX domain socket (spec §8) ---
# libpq expects a socket named "<dir>/.s.PGSQL.<port>" under -h <dir>
# (.claude/rules/pgwire.md); ExecDB's -s/--socket takes any path as-is.
cp "$BIN" "$WORK/pguds"
SOCK="$WORK/.s.PGSQL.15533"
pid=$(start_server "$WORK/pguds" -n -s "$SOCK" -q)
wait_for_socket "$SOCK"

out="$(psql -h "$WORK" -p 15533 -U any -d any -tAc 'SELECT 2;')"
[ "$out" = "2" ] || fail "psql UDS SELECT 2 returned $out"
pass "pgwire UDS: psql SELECT 2"

stop_server "$pid"
[ ! -e "$SOCK" ] || fail "UDS socket was not removed on graceful shutdown"
pass "pgwire UDS: socket removed on graceful shutdown"

# --- go install produces a binary where the footer/.overwrite mechanism
#     still works (.claude/rules/distribution.md) ---
GOBIN="$WORK/gobin"
mkdir -p "$GOBIN"
(cd "$ROOT" && GOBIN="$GOBIN" go install ./cmd/execdb)
[ -x "$GOBIN/execdb" ] || fail "go install did not produce a binary"
cp "$GOBIN/execdb" "$WORK/installed"
printf 'CREATE TABLE t(a INTEGER);\nINSERT INTO t VALUES (5);\n.overwrite\n' | "$WORK/installed" >/dev/null
out="$(printf 'SELECT * FROM t;\n.exit\n' | "$WORK/installed")"
echo "$out" | grep -qx '5' || fail "a go install-produced binary's .overwrite did not persist data"
pass "go install: footer/.overwrite mechanism works on an installed binary"

echo "e2e: all checks passed"
