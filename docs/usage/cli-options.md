# CLI options

*日本語版はこちら: [cli-options_ja.md](cli-options_ja.md)*

```
execdb [options]
```

| Short | Long | Type | Default | What it does |
| :--- | :--- | :--- | :--- | :--- |
| `-p` | `--pg-addr ADDR` | string | unset (disabled) | TCP listen address for the PostgreSQL wire protocol (e.g. `:5432`, `127.0.0.1:5432`). Omit to not start the external interface at all. |
| `-s` | `--socket PATH` | string | unset (disabled) | Also listen on this UNIX domain socket path (e.g. `/tmp/.s.PGSQL.5432`). `-p` and `-s` can be used together or independently. |
| `-u` | `--user NAME` | string | unset (Zero-Auth) | Require this username + a password for the external interface. See [Authentication](#authentication) below. Has no effect on the REPL, which is never authenticated. |
| `-o` | `--snapshot-as NAME` | string | unset | Default filename for `.snapshot` and for server-mode's auto-save on shutdown. |
| `-t` | `--timestamp` | bool | `false` | Append a `_YYYYMMDDHHMMSS` timestamp to saved filenames. See [Timestamped filenames](#timestamped-filenames). |
| `-i` | `--snapshot-interval DURATION` | duration | `0` (disabled) | Periodically save a snapshot at this interval (e.g. `5m`, `1h`). Works in both REPL and server mode. |
| `-n` | `--no-repl` | bool | `false` | Server mode: don't start the REPL, just run the external interface in the background. See [Server mode](#server-mode). |
| `-q` | `--quiet` | bool | `false` | Suppress the startup banner. |
| `-h` | `--help` | bool | — | Print usage and exit. |

At least one of `-p`/`-s` is required for the external interface to be
reachable at all; omitting both runs a REPL-only process with no network
listener.

## Authentication

By default ExecDB is Zero-Auth: anyone who can reach the listening
port/socket can connect, no credentials required. Passing `-u NAME` turns
on cleartext-password authentication for the external interface only (the
REPL itself is never authenticated — it already requires local process
access). The password is resolved in this order:

1. `EXECDB_PASSWORD` environment variable, if set — used with no prompt.
2. Otherwise, in REPL mode: an interactive `Password:` prompt at startup
   (masked input).
3. Otherwise, in server mode (`-n`): startup fails with an error — there is
   no terminal to prompt.

```sh
EXECDB_PASSWORD=secret execdb -p :5432 -u alice
```

```sh
psql -h 127.0.0.1 -p 5432 -U alice   # prompts for the password
```

## Server mode

`-n`/`--no-repl` runs ExecDB as a background process serving only the
external interface — no REPL, no stdin reading. Since there's no `.snapshot`
command available in this mode, ExecDB makes one exception to its normal
"save only on explicit command" rule: on `SIGTERM`/`SIGINT`, it saves a
snapshot (per `-o`/`-t`) and then exits.

```sh
execdb -n -p :5432 -o mydb &
kill %1   # saves to mydb (or mydb_<timestamp> with -t) before exiting
```

## Timestamped filenames

With `-t`/`--timestamp`, a save (`.snapshot`, `-o`, or server-mode
auto-save) gets a `_YYYYMMDDHHMMSS` suffix inserted before the extension,
replacing one if already present:

| Base filename | Result |
| :--- | :--- |
| `mydb` (no filename given, base is the running binary's name) | `mydb_20260901120000` |
| `mydb_20260101120000` (already timestamped) | `mydb_20260901120000` (old one replaced, not doubled) |
| `mydb.exe` | `mydb_20260901120000.exe` |

Without `-t` (the default), the filename is used as-is (extension `.exe`
still gets added automatically on Windows if omitted), so repeated saves
overwrite the same file.

## Periodic snapshots

`-i`/`--snapshot-interval` repeats a `.snapshot`-equivalent save on a
timer, in both REPL and server mode — a safety net against losing a long
session's work, or a simple periodic-backup mechanism in server mode.
Self-overwrite (`.overwrite`) has no periodic equivalent; only separate-file
saves are supported here, for the same reason `.overwrite` itself is a
deliberate, occasional operation rather than something to run unattended
on a timer.

```sh
execdb -n -p :5432 -i 5m -o mydb   # snapshots mydb every 5 minutes
```
