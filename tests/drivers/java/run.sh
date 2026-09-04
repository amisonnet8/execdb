#!/usr/bin/env bash
# Fetches the pgJDBC jar (if missing), compiles Check.java (if missing or
# stale) into out/ (gitignored), and runs it. Reused by tests/e2e.sh and
# CI's `drivers` job so both paths share one build step.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
JAR="$(bash "$DIR/fetch-jdbc.sh")"
OUT="$DIR/out"

if [ ! -f "$OUT/Check.class" ] || [ "$DIR/Check.java" -nt "$OUT/Check.class" ]; then
  mkdir -p "$OUT"
  javac -encoding UTF-8 -cp "$JAR" -d "$OUT" "$DIR/Check.java"
fi

java -cp "$JAR:$OUT" Check "$1"
