#!/usr/bin/env bash
# Restores Npgsql (via NuGet, first run only -- cached under
# ~/.nuget/packages, not inside this directory) and runs Check via
# `dotnet run`. Reused by tests/e2e.sh and CI's `drivers` job so both
# paths share one build step.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

dotnet run --project "$DIR" --configuration Release -- "$1"
