# tests/drivers

Cross-language driver checks for ExecDB's pgwire implementation (spec §8,
phase 4 Step 7). `tests/pgclient` (Go, pgx) already covers the protocol in
depth -- transaction isolation, `25P02`, disconnect/`CancelRequest`
cancellation. These checks exist for a narrower, different purpose: proving
that **other languages' mainstream PostgreSQL drivers, used with their own
default settings**, can connect to ExecDB and run basic SELECT/DDL-rejection/
transaction checks at all -- no ExecDB-specific workaround flags, matching
phase 4's stated goal.

| Directory | Driver | Runtime required |
| :--- | :--- | :--- |
| `python/` | psycopg2 (`python3-psycopg2`) | `python3` with `psycopg2` importable |
| `node/` | node-postgres (`pg`) | `node` + `npm` |
| `java/` | pgJDBC | `java` + `javac` (fetches the driver jar from Maven Central on first run) |

Each check connects, runs a small set of typed `SELECT`s, confirms DDL is
rejected with SQLSTATE `42501`, and does one INSERT+COMMIT+SELECT round
trip against table `t(a INTEGER)` (the same table `tests/e2e.sh` seeds for
`tests/pgclient`). On success each prints `OK` to stdout, mirroring
`tests/pgclient`'s own convention.

## Why these aren't wired into `go test`/`make check`

None of these runtimes are part of ExecDB's own build or a required
developer tool (`.claude/rules/testing.md`); they're installed on demand,
same as `psql` was in phase 1. `tests/e2e.sh` runs each check only if its
runtime is present on `PATH`, and prints a `skip -` line otherwise, exactly
like the existing PTY-only Ctrl+C check does when `script` isn't installed.
This is also why the Node dependency (`pg`) and the Java driver jar are
fetched into gitignored locations (`node/node_modules/`, `java/lib/`)
instead of being committed -- consistent with `.claude/rules/distribution.md`
not committing binaries into the repository.
