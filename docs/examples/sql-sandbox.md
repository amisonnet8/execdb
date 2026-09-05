# A zero-setup SQL sandbox

*日本語版はこちら: [sql-sandbox_ja.md](sql-sandbox_ja.md)*

For learning SQL, trying out a query idea, or a quick classroom/workshop
exercise, ExecDB gives you a full SQL engine (views, indexes, triggers,
transactions — real SQLite, not a toy subset) with nothing to install
beyond the single binary.

Grab the binary for your platform from the
[latest release](https://github.com/amisonnet8/execdb/releases/latest) (no
Go required), or `go install github.com/amisonnet8/execdb/cmd/execdb@latest`
if you have Go 1.26+:

```sh
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb> CREATE TABLE students(id INTEGER PRIMARY KEY, name TEXT, grade INTEGER);
execdb> INSERT INTO students(name, grade) VALUES ('Alice', 90), ('Bob', 75), ('Carol', 88);
execdb> SELECT name FROM students WHERE grade >= 85 ORDER BY grade DESC;
Alice
Carol
execdb> .mode column
execdb> .headers on
execdb> SELECT name, grade, CASE WHEN grade >= 90 THEN 'A' WHEN grade >= 80 THEN 'B' ELSE 'C' END AS letter FROM students;
name   grade  letter
-----  -----  ------
Alice  90     A
Bob    75     C
Carol  88     B
```

Everything is in memory and disappears when you exit — there's no
`DROP DATABASE` cleanup, no leftover container, no state to reset before the
next person uses the same terminal. If you want to keep what you built, one
command turns it into a file:

```
execdb> .snapshot my_lesson
Wrote my_lesson
execdb> .exit
```

`./my_lesson` (or `my_lesson.exe` on Windows) picks up exactly where you
left off, on any machine that runs it — no install, no setup, works the
same on Windows/macOS/Linux from a terminal or by double-clicking. See
[REPL commands](../usage/repl-commands.md) for the full command reference,
including `.import` for loading a CSV dataset to explore, and `.dump` for
printing the whole thing back out as SQL text.
