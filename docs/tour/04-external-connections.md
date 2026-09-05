# 4. Talking to it from other tools

*日本語版はこちら: [04-external-connections_ja.md](04-external-connections_ja.md)*

Everything so far has used the REPL directly. Add `-p ADDR` at startup and
ExecDB also speaks a PostgreSQL-compatible wire protocol on that address —
the REPL and the network listener share the same live data, at the same
time:

```sh
./mydb -p 127.0.0.1:5432
```

```
ExecDB v...
Loaded snapshot: mydb
Listening on 127.0.0.1:5432 (PostgreSQL wire protocol)
Enter ".help" for usage hints.
execdb>
```

Any Postgres client can now connect — `psql`, or a real language driver:

```sh
psql -h 127.0.0.1 -p 5432 -U any -d any -c 'SELECT * FROM todos;'
```

```
 id |      task      | done
----+----------------+------
  1 | write the tour |    0
  2 | ship it        |    0
(2 rows)
```

No user/password/database setup was needed — `any`/`any` above is
literally just filler text, accepted as-is. This is the same trick behind
the [mock API server example](../examples/mock-server.md), which also has
copy-pasteable connection snippets for Python, Node.js, Go, Java, .NET,
PHP, Ruby, Rust, and ODBC tools (Excel, Power BI, Access) — every one of
them works with that driver's own default settings, no ExecDB-specific
flags (Npgsql needs one extra connection-string option; see that page for
why).

## Read/write, but not schema changes

The external interface can run queries and modify rows, but not `CREATE`/
`ALTER`/`DROP` — DDL is reserved for people with local access to the REPL:

```sh
psql -h 127.0.0.1 -p 5432 -U any -d any -c 'CREATE TABLE hack(x INTEGER);'
```

```
ERROR:  DDL statements are not allowed via external interface
```

That's not a bug to route around; it's the access-control model this
interface is built on — see
[`docs/spec/execdb_spec.md`](../spec/execdb_spec.md) §2 for the full
rationale.

## Requiring a password

By default anyone who can reach the port can connect (Zero-Auth). Add
`-u NAME` to require a password too:

```sh
EXECDB_PASSWORD=secret ./mydb -p 127.0.0.1:5432 -u alice
```

```sh
PGPASSWORD=secret psql -h 127.0.0.1 -p 5432 -U alice -d any -c 'SELECT 1;'
```

A wrong password is rejected outright:

```
psql: error: connection to server ... failed: ERROR:  password authentication failed for user "alice"
```

This only guards the external interface — the REPL itself is never
authenticated, since it already requires being on the same machine. See
[Authentication](../usage/cli-options.md#authentication) for the full
password-resolution order.

## Running it unattended

`-n`/`--no-repl` drops the REPL entirely and just serves the external
interface, for running ExecDB as a background service. Since there's no
`.snapshot` command available in that mode, it makes one exception to
"never auto-save": it saves on shutdown.

```sh
./mydb -p :5432 -n &
kill %1
```

```
Saved snapshot to mydb
```

**Where to go next:** [`docs/usage/`](../usage/) for the full CLI-flag and
REPL-command reference, [`docs/examples/`](../examples/) for complete,
task-oriented walkthroughs (CI test databases, sharing a bug repro, a SQL
sandbox), or [`docs/spec/execdb_spec.md`](../spec/execdb_spec.md) for the
full design rationale behind everything in this tour.
