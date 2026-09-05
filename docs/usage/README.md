# Usage reference

Quick-reference documentation for running ExecDB and using it day to day.
This is index/table-driven material for looking things up, not a
walkthrough — if you're new to ExecDB, [`docs/tour/`](../tour/) is a
better starting point. For the *why* behind a given design (access
control, persistence model, wire protocol internals), see
[`docs/spec/execdb_spec.md`](../spec/execdb_spec.md).

- [CLI options](cli-options.md) — every startup flag (`-p`, `-n`, `-u`, ...)
- [REPL commands](repl-commands.md) — every dot-command (`.tables`,
  `.snapshot`, `.import`, ...)

For runnable, task-oriented walkthroughs (CI test databases, sharing a bug
repro, a mock API server, connecting from another language), see
[`docs/examples/`](../examples/).

## The short version

```sh
# Install (requires Go 1.26+)
go install github.com/amisonnet8/execdb/cmd/execdb@latest

# Run the REPL
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

`mydb` is now a standalone executable with that table and row baked in —
run it (`./mydb` on Linux/macOS, `mydb.exe` on Windows) and the data is
there. See [CLI options](cli-options.md) and [REPL commands](repl-commands.md)
for the full reference, or [`docs/examples/`](../examples/) for what to do
with it next.
