#!/usr/bin/env bash
# Installs node-postgres into node_modules/ (gitignored, see tests/drivers/
# README.md) on first run, then executes check.js. Reused by tests/e2e.sh
# and CI's `drivers` job so both paths share one install step.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ ! -d "$DIR/node_modules/pg" ]; then
  npm --prefix "$DIR" install --no-audit --no-fund >/dev/null
fi

node "$DIR/check.js" "$1"
