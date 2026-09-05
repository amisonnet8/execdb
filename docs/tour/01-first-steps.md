# 1. First steps: the REPL

Launch it with no arguments and you get an interactive SQL console, backed
by an empty in-memory database:

```sh
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb>
```

Anything you type that doesn't start with `.` is plain SQL — real SQLite,
not a toy subset. Create a table and put some rows in it:

```
execdb> CREATE TABLE todos(id INTEGER PRIMARY KEY, task TEXT, done INTEGER DEFAULT 0);
execdb> INSERT INTO todos(task) VALUES ('write the tour'), ('ship it');
execdb> SELECT * FROM todos;
1|write the tour|0
2|ship it|0
```

That `|`-separated, no-header output is `.mode list`, the default — built
for piping into `grep`/`awk`/scripts. For something more readable at a
terminal, switch modes:

```
execdb> .mode column
execdb> .headers on
execdb> SELECT * FROM todos;
id  task            done
--  --------------  ----
1   write the tour  0
2   ship it         0
```

`.mode column` turns headers on automatically, which is why the one line
above was enough to get both. There are three other output modes
(`csv`, `json`, `line`) for feeding results to other programs — see
[REPL commands](../usage/repl-commands.md#output-formatting) for all of
them.

Two commands for finding your way around a schema you didn't just type
yourself:

```
execdb> .tables
todos
execdb> .schema todos
CREATE TABLE todos(id INTEGER PRIMARY KEY, task TEXT, done INTEGER DEFAULT 0);
```

And to leave:

```
execdb> .exit
```

At this point everything you did is gone — there's no database file on
disk anywhere, and ExecDB never auto-saves. That's not a limitation to work
around; it's the whole point, and it's what the next chapter is about.

**Next:** [2. Snapshots: the binary *is* the data](02-snapshots.md)
