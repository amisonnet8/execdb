# Examples

*日本語版はこちら: [README_ja.md](README_ja.md)*

Task-oriented walkthroughs for common ways to use ExecDB. New to ExecDB
and want a guided, step-by-step introduction first? See
[`docs/tour/`](../tour/) instead — each page here is self-contained and
assumes you already know the basics. For a plain command/flag reference,
see [`docs/usage/`](../usage/); for the full design rationale, see
[`docs/spec/execdb_spec.md`](../spec/execdb_spec.md).

- **[CI/CD: an instant test database](ci-testing.md)** — bake a schema +
  seed data into a binary once, then have every test job just run it.
- **[Sharing a bug repro as a runnable file](snapshot-sharing.md)** —
  `.snapshot` a broken data state and hand someone the file instead of a
  list of setup steps.
- **[A zero-config mock API / database server](mock-server.md)** — point
  psql, psycopg, node-postgres, pgx, JDBC, Npgsql, PDO_PGSQL, the `pg` gem,
  the Rust `postgres` crate, or any ODBC tool (Excel, Power BI, Access) at
  ExecDB with no install, including copy-pasteable connection code for each.
- **[A zero-setup SQL sandbox](sql-sandbox.md)** — a full SQL engine
  (views/indexes/triggers/transactions) for learning or experimenting,
  nothing to install or clean up.

`tests/drivers/` and `tests/pgclient/` in the repository (run by
`make test`) are the more exhaustive, machine-checked cousins of the
connection examples here, if you want fuller reference code.
