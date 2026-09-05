# tests/drivers

*日本語版はこちら: [README_ja.md](README_ja.md)*

Cross-language driver checks for ExecDB's pgwire implementation (spec §8,
phase 4 Step 7). `tests/pgclient` (Go, pgx) already covers the protocol in
depth -- transaction isolation, `25P02`, disconnect/`CancelRequest`
cancellation. These checks exist for a narrower, different purpose: proving
that **other languages' mainstream PostgreSQL drivers**, used with their own
default settings wherever possible, can connect to ExecDB and run basic
SELECT/DDL-rejection/transaction checks at all, matching phase 4's stated
goal.

| Directory | Driver | Runtime required |
| :--- | :--- | :--- |
| `python/` | psycopg2 (`python3-psycopg2`) | `python3` with `psycopg2` importable |
| `node/` | node-postgres (`pg`) | `node` + `npm` |
| `java/` | pgJDBC | `java` + `javac` (fetches the driver jar from Maven Central on first run) |
| `dotnet/` | Npgsql | `dotnet` SDK (restores the Npgsql NuGet package on first run) |
| `odbc/` | psqlODBC (via pyodbc) | `unixodbc` + `odbc-postgresql` (the driver itself) + `python3` with `pyodbc` importable |
| `php/` | PDO_PGSQL (via PDO) | `php` with the `pdo_pgsql` extension loaded |
| `ruby/` | `pg` gem | `ruby` with the `pg` gem installed (`gem install pg` / `gem install --user-install pg`) |
| `rust/` | `postgres`/`tokio-postgres` crate | `cargo` (fetches the crate from crates.io on first run) |

**Npgsql is the one exception to "default settings":** it needs
`Server Compatibility Mode=NoTypeLoading` in its connection string
(supplied by `run-all.sh`, not hardcoded in `dotnet/Program.cs`) to
connect to ExecDB at all. Without it, Npgsql's connection bootstrap sends
a batch of SELECTs against Postgres system catalogs (`pg_type`, `pg_enum`,
...) plus a bare `SELECT version()` to build its own type catalog --
SQLite has none of those, so the very first connection attempt fails
before any application query runs. This is a standard, Npgsql-native
option for connecting to a wire-compatible-but-not-genuine-Postgres
backend (the same one CockroachDB/Redshift-style databases document for
Npgsql users), not an ExecDB-specific patch, and none of the other
verified drivers need an equivalent client-side flag.

**psqlODBC needs no client-side flag, but only connects because of
server-side work on ExecDB's end.** Its own connection bootstrap queries
the real Postgres system catalog (`pg_type`, checking for large-object
support), and its `SQLTables`/`SQLColumns` calls (used by `odbc/check.py`,
and by real schema-browsing ODBC consumers like Excel/Power BI/Access)
query `pg_class`/`pg_namespace`/`pg_attribute`/`pg_attrdef` plus the
`pg_get_expr()`/`current_schema()` built-in functions. ExecDB answers all
of these with a small pg_catalog-compatible set of temp views/functions,
set up once per pgwire connection -- see `cmd/execdb/pgcatalog.go`'s doc
comments for exactly what is (and is not) emulated, and
`.claude/rules/pgwire.md` for how this was discovered and why views
couldn't just live in a real attached `pg_catalog` database.

**PHP (PDO_PGSQL) and Ruby (`pg` gem) are, like psycopg2, thin wrappers
around libpq** -- the actual wire-protocol work all three do is identical
C code, so neither adds new protocol coverage on that front. They still
earn their own checks because of what sits *above* libpq: PDO_PGSQL
defaults to "emulated prepares" (`PDO::ATTR_EMULATE_PREPARES` true by
default for this driver), substituting parameter values into the SQL text
client-side and sending the result as plain Simple Query text -- a
materially different code path from every other verified driver (all of
which use real Extended Query parameter binding by default). Ruby's `pg`
gem, via `exec_params`, does use real Extended Query binding, exercising
that path independently of psycopg2's own specific usage pattern.

**Rust's `postgres`/`tokio-postgres` crate is a genuine, independent
reimplementation of the wire protocol** (no libpq involved), and its
stricter, statically-typed API surfaced two real ExecDB bugs beyond
anything the other 7 drivers found:

1. Its default (type-unaware) `query`/`execute` methods leave a
   parameter's OID unspecified and rely on the server's
   `ParameterDescription` to resolve it. Told 0 (ExecDB's "unspecified"
   answer, tolerated by every other driver), it queries
   `pg_catalog.pg_type`/`pg_range`/`pg_namespace` to look up what type 0
   supposedly is -- which doesn't exist, so it asks again, forever, until
   its stack overflows. `rust/src/main.rs` avoids this by using
   `prepare_typed` to self-declare each parameter's type up front (the
   same thing pgJDBC/Npgsql already do implicitly), the same fix a real
   Rust application would need.
2. A client that only ever Describes a *statement* (never a *portal*) --
   a valid, allowed message sequence tokio-postgres happens to use by
   default -- exposed a real inconsistency: `Execute` was recomputing the
   result column's type from the live query result instead of reusing
   whatever type the earlier `Describe` had already promised via
   `RowDescription`, and those two could legitimately disagree (a NULL
   trial-run's inferred type vs. a real bound value's). ExecDB now caches
   and reuses the Describe-time OID (`preparedStatement`/`portal`'s
   `resultOIDs`, `cmd/execdb/pgextended.go`) -- a fix that benefits every
   driver, not just this one.

Both are written up in full in `.claude/rules/pgwire.md`.

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
not committing binaries into the repository. The .NET package (Npgsql) is
restored by `dotnet` into its own NuGet cache (`~/.nuget/packages`, outside
this repo entirely); only the per-project `dotnet/bin/`/`dotnet/obj/` build
output directories need gitignoring. Rust's crate (`postgres`) is
similarly cached under `~/.cargo/registry`; only `rust/target/` (the
build output) is gitignored -- `rust/Cargo.lock` itself is committed, the
same as `go.sum`, for a reproducible dependency graph. PHP's `pdo_pgsql`
extension and Ruby's `pg` gem have no local build output at all to
gitignore (a system package and a user-installed gem, respectively).
