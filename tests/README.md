# tests

*日本語版はこちら: [README_ja.md](README_ja.md)*

Fixtures and scripts used by `make test` to exercise ExecDB end-to-end
(see `.claude/rules/testing.md`). Populated as implementation progresses
through the development phases in `PLAN.md`.

`pgclient/` (Go, pgx) is the only driver check that lives here. Checks
for other languages' PostgreSQL drivers (Python, Node.js, Java, .NET,
PHP, Ruby, Rust, ODBC) moved to the separate
[`execdb-drivers`](https://github.com/amisonnet8/execdb-drivers) repository.
