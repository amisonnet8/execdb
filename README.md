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
ecosystems (JDBC, psycopg, node-postgres, Npgsql, pgx, ...) work out of the
box.

**Status:** early development — no functional build yet. See
[`execdb_spec.md`](execdb_spec.md) for the full specification and
[`PLAN.md`](PLAN.md) for implementation progress.

日本語版は [README_ja.md](README_ja.md) を参照してください。

## License

MIT — see [LICENSE](LICENSE).
