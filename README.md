# ExecDB

Portable single-binary RDBMS in Go. SQLite-compatible SQL, zero setup, data
snapshots as executable files.

ExecDB keeps both the database engine and the data itself inside a single
executable file. There is no separate database file, no environment setup,
and no container volumes to mount: just run the binary. All operations run
against an in-memory SQLite-compatible engine (`modernc.org/sqlite`), and
data is persisted by writing out a new copy of the executable with the
latest in-memory state embedded in it — a "snapshot" you can run, share, or
commit like any other file.

External clients (ORMs, DB tools, language drivers) can connect over a
PostgreSQL-compatible wire protocol subset, so existing Postgres driver
ecosystems (JDBC, psycopg, node-postgres, Npgsql, pgx, ...) work with little
to no ExecDB-specific setup — see [`docs/spec/execdb_spec.md`](docs/spec/execdb_spec.md)
§8 for the one documented exception (Npgsql).

## Install

**Prebuilt binary** (no Go required): grab the file for your platform from
the [latest release](https://github.com/amisonnet8/execdb/releases/latest)
— e.g. `execdb_v0.1.1_linux_amd64`, `execdb_v0.1.1_darwin_arm64`,
`execdb_v0.1.1_windows_amd64.exe`. It's a single raw executable, not an
archive: download it, `chmod +x` it (not needed on Windows), and run it.

**From source** (requires Go 1.26+):

```sh
go install github.com/amisonnet8/execdb/cmd/execdb@latest
```

## Quickstart

```sh
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb> CREATE TABLE t(a INTEGER);
execdb> INSERT INTO t VALUES (1);
execdb> .snapshot mydb
Wrote mydb
execdb> .exit
```

`mydb` is now a standalone executable with that table and row baked in:

```sh
chmod +x mydb   # not needed on Windows
./mydb
```

```
ExecDB v...
Loaded snapshot: mydb
Enter ".help" for usage hints.
execdb> SELECT * FROM t;
1
```

Add `-p :5432` to also serve that data over the PostgreSQL wire protocol —
any Postgres client or driver can connect to it as-is.

## Learn more

- **[Interactive guide (Gemini Notebook)](https://notebook.google.com/notebook/f4c3426c-b394-4412-bb38-5d4174e2c636)**
  — an interactive Q&A notebook for exploring ExecDB
- **[`docs/tour/`](docs/tour/)** — a hands-on, step-by-step walkthrough for
  first-time users
- **[`docs/usage/`](docs/usage/)** — CLI flags and REPL command reference
- **[`docs/examples/`](docs/examples/)** — CI test databases, sharing a bug
  repro, a mock API server, a SQL sandbox
- **[`docs/spec/execdb_spec.md`](docs/spec/execdb_spec.md)** — full design
  and specification
- **[`PLAN.md`](PLAN.md)** — implementation progress

日本語版は [README_ja.md](README_ja.md) を参照してください。

## License

MIT — see [LICENSE](LICENSE).
