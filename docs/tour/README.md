# Tour

A hands-on walkthrough of ExecDB for people who haven't used it before.
Each chapter builds on the previous one and takes a few minutes — read
them in order with a terminal open, running the commands as you go.

If you already know what you're looking for, this isn't the place: see
[`docs/usage/`](../usage/) for a plain reference and
[`docs/examples/`](../examples/) for self-contained, task-oriented
walkthroughs instead.

1. **[First steps: the REPL](01-first-steps.md)** — start ExecDB, run some
   SQL, look around a schema.
2. **[Snapshots: the binary *is* the data](02-snapshots.md)** — the one
   idea that makes ExecDB different from "just run SQLite": persistence
   means writing out a new executable.
3. **[Getting data in and out](03-loading-data.md)** — CSV import, dumping
   to SQL text, pulling another snapshot's data into your current session.
4. **[Talking to it from other tools](04-external-connections.md)** —
   the PostgreSQL wire protocol, `psql`/driver access, what it will and
   won't let external clients do, and optional authentication.

Requires only the `execdb` binary — see the [top-level README](../../README.md)
for install options if you don't have it yet.
