# Sharing a bug repro (or any data state) as a runnable file

*日本語版はこちら: [snapshot-sharing_ja.md](snapshot-sharing_ja.md)*

When a bug only shows up with specific data in the database, the usual fix
is a wall of setup instructions ("run these 12 SQL statements, in this
order..."). With ExecDB, you share the *database itself*.

## Capture the state

From wherever you reproduced the bug:

```
execdb> .snapshot bug_123
Wrote bug_123
```

Send `bug_123` to a teammate (Slack, a shared drive, attached to the issue
— it's just a file). No instructions needed.

## Reproduce it

They run it exactly like any other executable:

```sh
chmod +x bug_123        # not needed on Windows
./bug_123
```

```
ExecDB v...
Loaded snapshot: bug_123
Enter ".help" for usage hints.
execdb>
```

Same tables, same rows, same state you had when the bug happened — nothing
to install, no dump/restore step, no version-of-Postgres mismatch to debug
first.

## Cross-platform sharing

`.snapshot`/`.overwrite` only ever act on the executable you're currently
running, so a snapshot taken on Linux is a Linux binary — it won't run on
Windows. To move the *data* (not the engine) to a different OS/architecture:

```sh
# On the target OS, starting from an empty ExecDB binary for that platform:
execdb-windows-amd64.exe
execdb> .load bug_123        # pulls in bug_123's data, not its engine
execdb> .overwrite            # writes that data into execdb-windows-amd64.exe itself
```

`.load` only ever reads the embedded *data* from the file you point it at —
never the other executable's engine code — so this works across any
combination of platforms ExecDB is built for. See
[CLI options](../usage/cli-options.md) and
[REPL commands](../usage/repl-commands.md) for the full reference on
`.snapshot`/`.load`/`.overwrite`.
