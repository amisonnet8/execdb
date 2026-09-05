# REPL commands

*日本語版はこちら: [repl-commands_ja.md](repl-commands_ja.md)*

Inside the interactive console, anything not starting with `.` is treated
as SQL (SQLite dialect — DDL, DML, and TCL `BEGIN`/`COMMIT`/`ROLLBACK` are
all available). Commands starting with `.` are ExecDB's own control
commands, modeled on the `sqlite3` CLI where it makes sense.

## Inspecting the database

| Command | What it does |
| :--- | :--- |
| `.tables` | List table names. |
| `.schema [TABLE]` | Show `CREATE` statements (all objects, or just `TABLE`'s). |
| `.help` | List all dot-commands. |

## Output formatting

| Command | What it does |
| :--- | :--- |
| `.mode MODE` | Set the output format: `list` (default, `\|`-separated, no header), `column` (aligned columns, auto-enables `.headers`), `csv` (RFC 4180, CRLF line endings), `json` (array of row objects; BLOBs as hex strings), `line` (one `name = value` per line). |
| `.headers on\|off` | Show/hide column-name headers. Switching to `.mode column` turns this on automatically; toggle it off afterward if you don't want it. |

```
execdb> .mode column
execdb> .headers on
execdb> SELECT * FROM users;
id  name
--  -----
1   Alice
2   Bob
```

## Saving and loading data

| Command | What it does |
| :--- | :--- |
| `.snapshot [FILENAME] [--timestamp]` | Write the current in-memory state out as a new standalone executable. Defaults to the running binary's name (or `-o`'s value); `--timestamp` appends a timestamp (see [CLI options](cli-options.md#timestamped-filenames)). |
| `.overwrite` | Write the current state into *this* running executable, then exit. The one exception to "no auto-save on exit" (§4 of the spec) — a deliberate "edit and close" workflow. |
| `.load FILE` | Replace the in-memory database with another ExecDB file's embedded data (not its engine). Does **not** write a file itself — follow with `.snapshot`/`.overwrite` to persist it. |
| `.dump [PATTERN]` | Print schema + data as replayable SQL (optionally filtered to tables matching a `LIKE` pattern). Piping this into another `execdb` process recreates the same state. |
| `.import FILE TABLE` | Bulk-load a CSV file. Creates `TABLE` (all-TEXT columns) from the header row if it doesn't exist yet; if it does, the first row is treated as data too. Always reads CSV regardless of `.mode`. A row with the wrong number of fields aborts the whole load (no partial import) — this is a deliberate difference from `sqlite3`, which warns and continues. |

```
execdb> .snapshot bug_123 --timestamp
Wrote bug_123_20260901120000
```

## Ending the session

| Command | What it does |
| :--- | :--- |
| `.exit [CODE]` / `.quit [CODE]` | Exit. No save prompt, no auto-save — data is gone unless you already ran `.snapshot`/`.overwrite`. With `CODE`, exits immediately with that process exit code. |

Ctrl+D (EOF) at the prompt behaves the same as `.exit`. Ctrl+C in an
interactive terminal is a `sqlite3`-style *interrupt*, not an unconditional
quit: it cancels a running query if one is in flight, discards a partially
typed multi-line statement if idle, and only force-quits (exit code 1) on
two consecutive presses with no input read in between. Piped/scripted input
(no TTY) doesn't get this handling at all — Ctrl+C there is the OS default
(immediate termination), so a script that starts `execdb` can still be
killed with Ctrl+C as expected.

## Not included

A few `sqlite3` commands are deliberately not part of ExecDB, since they
assume a multi-database or file-based model ExecDB doesn't have: `.open`/
`.databases` (no database switching — there is exactly one, in memory),
`.backup`/`.restore` (superseded by `.snapshot`), `.session` (change-tracking
sessions).
