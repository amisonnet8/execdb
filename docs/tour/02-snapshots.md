# 2. Snapshots: the binary *is* the data

This is the one idea that makes ExecDB different from "just run SQLite":
**persistence means writing out a new executable**, not a `.db` file next
to it. One command does it:

```
execdb> CREATE TABLE todos(id INTEGER PRIMARY KEY, task TEXT, done INTEGER DEFAULT 0);
execdb> INSERT INTO todos(task) VALUES ('write the tour'), ('ship it');
execdb> .snapshot mydb
Wrote mydb
execdb> .exit
```

`mydb` is not a database file that some separate `execdb` program reads —
it's a full, standalone copy of the engine itself, with your table and rows
baked into it:

```sh
chmod +x mydb   # not needed on Windows
./mydb
```

```
ExecDB v...
Loaded snapshot: mydb
Enter ".help" for usage hints.
execdb> SELECT * FROM todos;
1|write the tour|0
2|ship it|0
```

Copy `mydb` to another machine, a Docker image, a CI job — there's nothing
else to install, mount, or configure. It's a regular executable file; treat
it like one (commit it, `scp` it, attach it to a bug report).

## Editing a snapshot in place

Add more data, then fold it back into the same file you're running with
`.overwrite`:

```
execdb> INSERT INTO todos(task, done) VALUES ('take a break', 1);
execdb> .overwrite
Overwrote the running executable.
```

`.overwrite` exits automatically once it's done — there's no need for a
separate `.exit` after it. Run `./mydb` again and the new row is there too.

## What doesn't happen automatically

`.exit`/`.quit` never prompt to save and never auto-save — if you didn't
run `.snapshot`/`.overwrite`, your changes are gone. This is deliberate
(scripts and CI runs shouldn't ever block on a save prompt), not a missing
feature. If you want a safety net for a long session, `-i`/`--snapshot-interval`
saves on a timer instead — see [CLI options](../usage/cli-options.md#periodic-snapshots).

**Next:** [3. Getting data in and out](03-loading-data.md)
