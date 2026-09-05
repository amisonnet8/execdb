# 3. Getting data in and out

## Loading a CSV

`.import` bulk-loads a CSV file into a table, creating it from the header
row if it doesn't already exist:

```
execdb> .import sample.csv students
Inserted 3 rows into "students".
execdb> SELECT * FROM students;
Alice|90
Bob|75
Carol|88
```

The created table is all-`TEXT` columns (that's what a CSV header gives
you to work with); `CAST`/arithmetic in your `SELECT`s handles the rest if
you need numbers. A row with the wrong number of fields aborts the whole
import rather than skipping it — see
[REPL commands](../usage/repl-commands.md#saving-and-loading-data) for the
exact rules.

## Dumping it back out as SQL

```
execdb> .dump
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE "students"("name" TEXT,"grade" TEXT);
INSERT INTO "students" VALUES('Alice','90');
INSERT INTO "students" VALUES('Bob','75');
INSERT INTO "students" VALUES('Carol','88');
COMMIT;
```

That's plain, replayable SQL text — pipe it into another `execdb` process
(or save it to a `.sql` file) to recreate the same state elsewhere. `.dump`
takes an optional `LIKE` pattern if you only want some of the tables.

## Pulling in another snapshot's data

`.load` is different from just running a snapshot directly: it replaces
*only the in-memory data* of the database you already have open, using
**your currently running engine**, not the engine embedded in the file you
load from.

```
execdb> .load mydb
Loaded data from mydb
execdb> SELECT * FROM todos;
1|write the tour|0
2|ship it|0
3|take a break|1
```

That distinction matters when you've built (or received) several snapshots
over time and want to merge, compare, or migrate their data under one
engine, rather than juggling several separate executables. `.load` doesn't
write anything to disk by itself — follow it with `.snapshot`/`.overwrite`
if you want to keep the result.

**Next:** [4. Talking to it from other tools](04-external-connections.md)
