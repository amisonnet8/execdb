#!/usr/bin/env bash
# Downloads the pgJDBC driver jar into lib/ (gitignored, see tests/drivers/
# README.md) from Maven Central if it isn't already there. Pinned to an
# exact version for reproducibility, same rationale as this repo's GitHub
# Actions "uses:" references (.claude/rules/testing.md's CI pitfalls note).
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION="42.7.4"
JAR="$DIR/lib/postgresql-$VERSION.jar"

if [ ! -f "$JAR" ]; then
  mkdir -p "$DIR/lib"
  curl -sfL -o "$JAR" \
    "https://repo1.maven.org/maven2/org/postgresql/postgresql/$VERSION/postgresql-$VERSION.jar"
fi

echo "$JAR"
