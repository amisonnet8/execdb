#!/usr/bin/env bash
# tests/drivers/run-all.sh -- runs every available driver check
# (tests/drivers/README.md: python/node/java) against a throwaway ExecDB
# server seeded with table t(a INTEGER), skipping any driver whose runtime
# isn't installed. Shared by tests/e2e.sh (make test) and CI's `drivers`
# job (.github/workflows/test.yml) so both paths exercise identically,
# rather than duplicating the server-setup logic in two places.
#
# Usage: run-all.sh [BIN] [PORT]
#   BIN  path to an ExecDB binary to copy and seed (default: bin/execdb,
#        built by `make build`). The original is never touched -- this
#        script always operates on a scratch copy (tests/e2e.sh's own rule
#        for anything involving .overwrite, .claude/rules/testing.md).
#   PORT TCP port for the throwaway server (default: 15538).
set -euo pipefail

DRIVERS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$DRIVERS_DIR/../.." && pwd)"
BIN="${1:-$ROOT/bin/execdb}"
PORT="${2:-15538}"

[ -x "$BIN" ] || { echo "run-all.sh: $BIN not found or not executable; run 'make build' first" >&2; exit 1; }

WORK="$(mktemp -d)"
pid=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

cp "$BIN" "$WORK/pgdrivers"
printf 'CREATE TABLE t(a INTEGER);\n.overwrite\n' | "$WORK/pgdrivers" >/dev/null

(cd "$WORK" && exec "$WORK/pgdrivers" -n -p "127.0.0.1:$PORT" -q) >/dev/null 2>&1 &
pid=$!

tries=100
while ! (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; do
  tries=$((tries - 1))
  [ "$tries" -gt 0 ] || { echo "run-all.sh: timed out waiting for 127.0.0.1:$PORT to accept connections" >&2; exit 1; }
  sleep 0.1
done
exec 3>&- 3<&- 2>/dev/null || true

status=0

if command -v python3 >/dev/null 2>&1 && python3 -c 'import psycopg2' >/dev/null 2>&1; then
  if python3 "$DRIVERS_DIR/python/check.py" "host=127.0.0.1 port=$PORT user=any dbname=any"; then
    echo "ok - tests/drivers/python (psycopg2)"
  else
    echo "FAIL - tests/drivers/python (psycopg2)" >&2
    status=1
  fi
else
  echo "skip - tests/drivers/python (python3/psycopg2 not available)"
fi

if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  if bash "$DRIVERS_DIR/node/run.sh" "postgres://any@127.0.0.1:$PORT/any"; then
    echo "ok - tests/drivers/node (node-postgres)"
  else
    echo "FAIL - tests/drivers/node (node-postgres)" >&2
    status=1
  fi
else
  echo "skip - tests/drivers/node (node/npm not available)"
fi

if command -v java >/dev/null 2>&1 && command -v javac >/dev/null 2>&1; then
  if bash "$DRIVERS_DIR/java/run.sh" "jdbc:postgresql://127.0.0.1:$PORT/any?user=any"; then
    echo "ok - tests/drivers/java (pgJDBC)"
  else
    echo "FAIL - tests/drivers/java (pgJDBC)" >&2
    status=1
  fi
else
  echo "skip - tests/drivers/java (java/javac not available)"
fi

exit "$status"
