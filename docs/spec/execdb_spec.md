# 🗃️ ExecDB Specification (Draft)

*日本語版はこちら: [execdb_spec_ja.md](execdb_spec_ja.md)*

**ExecDB** is an environment-setup-free, portable single-binary RDBMS that keeps both the database engine and the data itself inside one executable file.

**License:** MIT

---

## 1. Core concepts

* **Data-in-Binary (single-binary self-containment):** The executable holds its own data region, eliminating any need for a separate DB file, elaborate environment setup, or Docker volume mounts.
* **In-Memory Operations:** All data is unpacked into memory at startup, so every query runs at memory speed (ultra-low latency).
* **Snapshot Persistence (a.k.a. writing out a differently-named executable):** On shutdown (or on an explicit save instruction while running), the latest in-memory data is persisted by generating a **new executable file (under a different name)** that embeds it. Keeping the original executable untouched makes safe snapshotting and data distribution easy.
* **Zero-Auth & Lightweight:** ExecDB strips away the concepts of user management, permissions, and login entirely, pursuing frictionless usability for local development, testing, and container environments. Zero-Auth is the default, but `--user` (§9) can opt in to lightweight authentication (a single name+password key — there is no concept of user management).

**CLI output language:** Strings the CLI emits — REPL messages, error messages, `--help`, log output, etc. — are, as a rule, in **English**, since ExecDB is meant to be published and used as an international OSS project.

**Log destination:** Log/diagnostic messages go to **stderr only**; there is no log-file generation or rotation mechanism. This keeps with the core concept of self-containment in a single binary — the design deliberately avoids producing log files as a side effect. REPL query results go to stdout, so the two are cleanly separated. Persisting logs long-term when running in server mode (`--no-repl`) is left to redirection (e.g. `2> execdb.log`) or existing operational infrastructure (Docker/systemd/etc.). `-q`/`--quiet` (§9) can suppress this output entirely.

---

## 2. Interfaces and access control

For safe operation and simple access control, the kinds of queries allowed (access rights) are clearly separated by access path.

| Interface | Role | Permitted operations |
| :--- | :--- | :--- |
| **Interactive console (REPL)** | Started directly on the terminal at launch. For schema definition and data management. | **DDL**, **DML**, **TCL**, dedicated control commands |
| **External I/F (PostgreSQL-compatible wire protocol)** | Listens in the background. For data manipulation from applications and external tools. | **DML**, **TCL** *(DDL is rejected)* |

### Query category definitions
* **DDL (Data Definition Language):** `CREATE TABLE`, `DROP TABLE`, `ALTER TABLE`, `CREATE VIEW`, `CREATE INDEX`, `CREATE TRIGGER`, etc.
* **DML (Data Manipulation Language):** `SELECT`, `INSERT`, `UPDATE`, `DELETE`, etc.
* **TCL (Transaction Control Language):** `BEGIN`, `COMMIT`, `ROLLBACK`

### Concurrency control
The REPL and the external I/F are treated as two independent clients of the internal SQL engine. ExecDB does not implement its own concurrency control (locking, transaction isolation levels, etc.); it defers entirely to whatever mechanisms the internal SQL-compatible engine (chapter 7) provides out of the box. The ExecDB-side access-control layer (this chapter's allow/deny rules) and the engine-side exclusion-control layer are kept as separate responsibilities.

**How this is achieved:** each REPL/external-I/F connection corresponds to a dedicated SQLite connection returned by the `engine` package's `DB.Session(ctx)` (internally, a distinct connection pointing at the same in-memory DB over `modernc.org/sqlite`'s `memdb` VFS — see chapter 7). `BEGIN`/`COMMIT`/`ROLLBACK` are executed as plain SQL statements on that connection, and any waiting/failure from lock conflicts with other connections is left to SQLite's own busy-handler mechanism (`busy_timeout`) — ExecDB never implements its own locking, queueing, or inter-connection arbitration. The Go-level exclusion (`sync.RWMutex`) that `engine.DB` itself holds exists only to protect the DB's own lifecycle/metadata (connection swapping, `Close`, the information `Info()` returns) — it is not concurrency control for SQL execution itself (the two are clearly separate layers, and this does not contradict the principle above).

### `SET`/`SHOW` and other session commands (a third category)

Statements arriving over the external I/F include some that the above two-way split ("DML/TCL allowed, DDL rejected") cannot describe. PostgreSQL drivers (pgJDBC in particular) automatically send session-parameter-setting commands like `SET extra_float_digits = 3` right after connecting, but SQLite has no `SET`/`SHOW` statements at all — passing them straight to the internal SQL engine as-is produces a syntax error, and the connection itself can never be established.

`SET`/`SHOW` are treated as a **third category** that is neither "allowed and passed to SQLite" nor "rejected like DDL." The external-I/F layer intercepts them before they reach the internal SQL engine and simply keeps the value in an in-memory, per-connection map (SQLite's own settings are never actually changed): `SET` gets a `CommandComplete("SET")`, and `SHOW` gets its value back as a one-column, one-row result. Neither returns an `ErrorResponse` the way DDL rejection does — from the driver's point of view, the command needs to look like it succeeded normally. See §8 for details.

---

## 3. REPL command set

The interactive console (REPL) can run dedicated control commands (dot-commands) starting with `.`, in addition to SQL statements. The command set follows the `sqlite3` CLI, adapted to fit ExecDB's single-in-memory-DB concept.

### Basic commands adopted (following SQLite)

| Command | Role |
| :--- | :--- |
| `.tables` | List table names |
| `.schema [table]` | Show `CREATE` statements (schema) |
| `.exit [CODE]` / `.quit [CODE]` | Exit the REPL (no auto-save, and no save-confirmation prompt either — exits immediately. See §4. Without `CODE`, exits normally; with it, exits the process immediately with that code) |
| `.help` | List commands |
| `.headers on\|off` | Whether to show column names in results |
| `.mode MODE` | Output format. Only five are adopted: `list` (default, `\|`-separated, no header) / `column` (aligned column widths, auto-enables `.headers` when switched to) / `csv` (RFC 4180, CRLF) / `json` (array; BLOBs as hex strings) / `line` (one `name = value` per line). Decorative modes (`quote`/`insert`/`tabs`/`markdown`/`box`/`html`, etc.) are not adopted, per the CLI output policy (`.claude/rules/cli-output.md`) |
| `.import FILE TABLE` | Load a CSV file into a table. **Always reads it as CSV regardless of the `.mode` setting** (sqlite3 ties this to `.mode`, but ExecDB deliberately simplifies to "`.import` is always CSV" — an intentional difference). If `TABLE` doesn't exist, it's auto-`CREATE`d from the first line as column names, all-`TEXT`; if it exists, the first line is treated as data too. **If a row's field count doesn't match the column count, the whole load is aborted with an error naming that row number, and nothing is inserted** (sqlite3 warns and continues by padding/truncating, but for ExecDB's primary use case — seeding data — silently inserting corrupted data was judged more harmful than aborting outright — an intentional difference) |
| `.dump [PATTERN]` | Dump the schema and data of tables matching `PATTERN` (a SQL LIKE pattern; all tables if omitted), along with any indexes/views/triggers belonging to them, as SQL statements. Literal-value quoting is delegated to SQLite's own `quote()` function |

`.import`/`.dump` are adopted not only for external data interchange but also because they're **useful during development/testing for seeding data and for checking a snapshot of the current state**.

### Commands ExecDB adds on its own

| Command | Role |
| :--- | :--- |
| `.snapshot [FILENAME] [--timestamp]` | Save a snapshot (see §4, §9). With `--timestamp`, a timestamp is appended to the filename |
| `.overwrite` | Overwrite this executable's own file with the latest data, and exit the REPL on success (see §4, §7) |
| `.load <FILENAME>` | Pull in only the data from another ExecDB file, replacing the in-memory DB state (produces no file. See §4) |

### Commands not adopted

Since ExecDB always deals with a single, simple in-memory DB, the following SQLite-derived commands are not adopted.

* `.open` / `.databases` (no concept of switching between multiple DBs)
* `.backup` / `.restore` (role overlaps with ExecDB's own `.snapshot` mechanism)
* `.session` (change-tracking session feature; excluded as excessive for this scope)

---

## 4. Persistence specification (Snapshot Mechanism)

Data is retained by **"generating a new executable file (under a different name)."**

* **When it happens:**
  * **Only on an explicit `.snapshot` command:** a snapshot is generated only when manually instructed, e.g. from the interactive console.
  * **No auto-save on process shutdown:** if a `SIGTERM`, or a Ctrl+C (`SIGINT`) that actually terminates the process (see §10 for the detailed interactive-mode Ctrl+C behavior — it does not always exit immediately), arrives, the in-memory data is not saved and is simply lost. ExecDB assumes the volatility of an in-memory DB as a given, and consolidates persistence into a single, explicit user action (`.snapshot`) to avoid the risk of corruption from a write interrupted mid-shutdown, and to avoid unwanted proliferation of automatic snapshots.
* **Filename convention:**
  * By default, an executable with a date/time timestamp appended is auto-generated (e.g. `execdb_20260831_143000`).
  * Command-line arguments or interactive-console instructions can also specify an arbitrary filename, or overwrite an existing file.
* **Managing snapshot files:** since each `.snapshot` freshly generates a full copy of the engine plus data, the number of files and total size grow with every run. Cleaning up (deleting) old snapshots is out of scope for the spec and left to operational (user) judgment.

### The exception of self-overwrite (`.overwrite`)

`.overwrite` is, like `.snapshot`, a save triggered by an explicit user action, but it differs in that **it terminates the process at the same time it saves**. It is the sole deliberate exception to the "no auto-save on process shutdown" principle above, intentionally permitted to provide an intuitive "edit and just close" workflow for learning use cases and lightweight CLI tools. See §7 for the write mechanism (the steps that avoid overwriting the file currently being executed).

### Pulling data in from another file (`.load`)

`.load <FILENAME>` is a command that pulls in **only the data embedded in another ExecDB file** and **replaces the currently running process's in-memory DB state with it**. It produces no file (it only changes in-memory state). Its main use case is porting data between ExecDB files built for different OS/architectures (e.g. taking data created on Linux and pulling it into an empty Windows binary before distributing it).

* **Behavior:** it reads only the data blob (see §7) out of `<FILENAME>` and pulls it into the SQLite engine (see §7 for the internal implementation), replacing the currently running process's in-memory DB state. Whatever data the running process originally had is **completely replaced** (not merged).
* **Effect on files:** `.load` itself produces no file. To persist the pulled-in state as a file, follow it with `.snapshot` (save under a different name) or `.overwrite` (self-overwrite). The typical flow for porting data to a binary for a different OS is: launch the empty binary for the target OS → pull the data into memory with `.load <FILENAME>` → write it into that binary itself with `.overwrite`.
* **The engine itself is unchanged:** `.load` never references the engine portion of the source file at all. It always reads only the data, using the currently running process's own engine. It is not possible to directly pull in the engine portion of a binary built for a different OS.
* **Effect on existing sessions:** since `.load` replaces the in-memory DB as an overwrite of the live database (see §7), any other session already open at that point (the REPL itself, another external-I/F connection, an `engine.Session`) keeps its connection intact rather than losing it, and any statement issued afterward sees the replaced data as-is. However, if another session has an uncommitted write transaction in flight while `.load` runs, `.load` may block or fail, per SQLite's own exclusion control (see "Concurrency control" in this chapter).
* **`.load`ing a file with no data:** if the source file has no ExecDB data blob (the footer's `Magic` is missing, or the data length is 0), it's an error, and the currently running process's in-memory state is left unchanged.
* **Handling a version mismatch:** if the source file's footer (`Version` field, see §7 — the data-blob format version, a value distinct from the ExecDB software's own version, e.g. a Git tag) differs from the `Version` the currently running process would write in its own footer, processing continues after displaying a warning (it is not rejected). Displaying this warning is the responsibility of `cmd/execdb` (the caller) — the `engine` package itself never logs anything (per the division of responsibility in §6: the library never writes to stderr). `engine` provides an `Inspect(path)` API that reads only the footer information, so `cmd/execdb` calls it before running `.load` and prints its own message if it detects a version mismatch.
* **Relationship to the §4 principle:** like `.snapshot`/`.overwrite`, `.load` too is only ever run by an explicit user action. There is no mechanism that automatically reads data from another file. Note that `.load` itself only changes in-memory state and does not persist to a file (a separate `.snapshot`/`.overwrite` is required to save it).

---

## 5. Main use cases

1. **Blazing-fast CI/CD & E2E testing (Instant Test DB)**
   * Just drop in a single binary with its tables already built and seed data already loaded — a test environment spins up in memory in under a second, with no cleanup needed afterward.
2. **Sharing a bug repro "with its data state attached," across a team (Executable Snapshots)**
   * Turn the data state where a bug occurred into a binary with `.snapshot bug_123` and share it. Whoever receives it just runs it to instantly reproduce the exact same DB environment.
   * If the recipient is on a different OS, launch the empty binary for that OS, pull the data into memory with `.load`, then run `.overwrite` to rebuild the same data as an executable for that OS (see §4).
3. **A mock API / demo environment with zero environment setup (Zero-Config Mock Server)**
   * Build up data in the interactive console, then expose it as-is over the external I/F (PostgreSQL-compatible protocol). Existing DB client tools and ORMs can connect directly — ideal for frontend development or client demos.
4. **Portable state management for edge/CLI tools (Portable Data Capsules)**
   * Achieve fast processing and file-level snapshot saving with a single executable, no dependency on an external DB.
5. **Zero-setup SQL learning/hands-on sessions (Zero-Setup SQL Sandbox)**
   * With no install or authentication setup, just run the binary to instantly learn/experiment with full-featured SQL (views/indexes/triggers/transactions).
   * Since double-clicking the executable (or launching it from a terminal) starts the REPL on Windows, macOS, or Linux alike, even beginners can try it immediately with no extra setup like WSL2.

---

## 6. Library architecture

ExecDB is built around an **in-memory SQL engine library (the `engine` package)** as its core, with the current standalone executable (a single-binary RDBMS combining REPL + external I/F + an embedded binary) positioned as "one reference implementation" built using that `engine`.

### Layer structure

```
execdb/
├── engine/              ← [Library core] the in-memory SQL engine
│                            (a modernc.org/sqlite wrapper, a Go-function-call
│                             API, a persistence interface, and an Overwrite
│                             that overwrites its own executable)
│
└── cmd/execdb/          ← [App] the single-binary RDBMS built on engine
                             (REPL, external I/F (PostgreSQL-compatible wire
                             protocol), and DDL/DML/TCL access control (§2))
```

The concrete file layout (`engine.go`, `persist.go`, `main.go`, `repl.go`, `pgwire.go`, etc.) is decided during implementation (see `.claude/rules/directory-structure.md` for details). This section only shows the division of responsibility between the two directories.

### Division of responsibility

| | `engine` (library) | `cmd/execdb` (app) |
| :--- | :--- | :--- |
| SQL execution | Provides Go function calls (one-shot execution via `db.Query()`/`db.Exec()`, etc., and `db.Session(ctx)` for a single independent client spanning `BEGIN`/`COMMIT`) | Just calls `engine`'s API |
| Network I/F | **Has none** (no direct dependency on `net`/`net/http`) | Implements the PostgreSQL-compatible wire protocol |
| Access control (DDL/DML/TCL) | Unrestricted (caller code is assumed trusted) | Controlled per access path — REPL vs. external I/F (§2) |
| Persistence | Generic read/write (any file, `io.Writer`/`io.Reader`) + overwriting its own executable (`Overwrite`) | Just calls it from `.snapshot`/`.overwrite`/`.load` commands (§4) |
| Purpose | An embedded DB layer for other Go apps to build in (a substitute for a sqlite file) | CI/CD testing, mock APIs, portable distribution |

### Design principle: safety from having no network I/F

The `engine` package can only be accessed via Go function calls. Since it implements no network-based I/O, when embedded in another Go app, there is **no external communication path at all unless that app itself explicitly creates one**. This is not "safety through logic that rejects things," but "safety through the structural absence of any means to be reached" — and the fact that `engine` itself never imports `net`/`net/http` is verifiable at the code level (this design is consistent with the "Zero-Auth" philosophy in chapter 1). Note, however, that the `net` package does appear as a transitive dependency (via `modernc.org/libc`, which the internal SQL engine `modernc.org/sqlite` uses). That's an artifact of the SQLite driver implementation itself, not a sign that `engine` has code that performs network I/O — what should be verified is "whether there's a direct import" and "that `net/http` never appears, even transitively."

### Self-overwrite (`Overwrite`)

The `engine` package provides a function for overwriting the executable of the host app that embeds it, with the latest DB state.

```go
// engine package
// Overwrite overwrites the caller process's own executable (the path
// obtained via os.Executable()) with content that embeds the current DB
// state. See §7 for the write mechanism.
// On success the file's contents are replaced, but the caller process
// itself does not exit (whether to exit is left to the caller's judgment).
func (db *DB) Overwrite() error
```

`engine` never makes the host app aware of "where the engine ends and the data begins." The entire host app (the whole app binary that embeds `engine`) is treated as-is as "the engine portion," with data and a footer appended after it. ExecDB's own `.overwrite` command (§3) is itself just a thin wrapper that calls this `Overwrite` internally.

**Example usage (host app side):**
```go
if err := db.Overwrite(); err != nil {
    log.Fatal(err)
}
os.Exit(0) // realizes an "edit, close, and pick up where you left off next time" experience
```

**Caveats when using it:**
* If the host app is installed in a directory that requires elevated write permissions, like `/usr/bin/` or `C:\Program Files\`, `Overwrite` fails with a permission error.
* If the host app has its own separate self-update mechanism (Squirrel/Sparkle, etc.), be careful — the timing of the executable replacement could conflict.
* This API's scope is embedding into Go apps. Porting the same logic to an app in another language is possible, but is out of scope for this spec.

### `OpenSelf` — loading from one's own executable

For host apps like `cmd/execdb` itself that need to "load the data embedded in their own executable," `engine` provides a dedicated entry point. Internally it's a thin wrapper that just gets its own path via `os.Executable()` and calls `Open`, but it also does a best-effort deletion, at startup, of the `.execdb_old` staging file (see §7) that a previous `Overwrite` may have left behind.

```go
// engine package
// OpenSelf loads the data embedded in the currently running process's own
// executable (os.Executable()).
func OpenSelf() (*DB, error)
```

### `Session` — a dedicated connection per independent client

`db.Query()`/`db.Exec()` are one-shot calls that borrow a connection from the pool for a single statement and return it; even if you `BEGIN` through it, there's no guarantee a later `COMMIT` reaches the same connection (see "Concurrency control" in chapter 2). For a use case like the REPL or the external I/F — "one independent client spans a transaction across multiple statements on the same connection" — use `Session`.

```go
// engine package
// Session returns a dedicated connection to the in-memory DB that db
// holds. It's meant to be obtained once per independent client (one
// external-I/F connection, the REPL process itself, etc.). BEGIN/COMMIT/
// ROLLBACK can be run as ordinary SQL statements on this connection —
// there is no dedicated transaction API on the Go side.
func (db *DB) Session(ctx context.Context) (*Session, error)

func (s *Session) Exec(query string, args ...any) (sql.Result, error)
func (s *Session) Query(query string, args ...any) (*sql.Rows, error)
func (s *Session) QueryRow(query string, args ...any) *sql.Row
// *Context variants of each of the above exist too, as do *Context
// variants of db.Exec, etc. themselves.

// For a caller that repeatedly executes the same statement (e.g.
// cmd/execdb's .import bulk-loading CSV data), returns a *sql.Stmt
// prepared on this Session's dedicated connection.
func (s *Session) Prepare(query string) (*sql.Stmt, error)
func (s *Session) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)

// Close explicitly once done with it (idempotent).
func (s *Session) Close() error
```

### Example usage as a library

```go
import "github.com/amisonnet8/execdb/engine"

// Open a dedicated data file (used as a substitute for a sqlite file)
db, err := engine.Open("myapp.execdb")

// Ordinary SQL operations (no DDL/DML restrictions, since in-app code is
// assumed trusted)
db.Exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT)")
db.Query("SELECT * FROM users")

// Persistence (explicitly write a snapshot to a separate file; the caller
// specifies the filename — naming conventions like timestamping are
// cmd/execdb's responsibility)
db.Snapshot("myapp_backup.execdb")

// Persistence (overwrite its own executable; this always targets
// os.Executable(), regardless of the path passed to Open())
db.Overwrite()

db.Close()
```

---

## 7. Architecture and technology choices

| Item | Content | Notes |
| :--- | :--- | :--- |
| **Language** | **Go (Golang)** | Static binary generation, cross-compilation, multi-platform support. |
| **Target OS** | **Cross-platform (Linux / macOS / Windows)** | Standalone REPL launch and the external I/F are guaranteed to work identically across all OSes. UNIX Domain Socket is an additional option for local connections (availability on Windows is environment-dependent — Windows 10 Build 17063+/Server 2019+ as a rule of thumb). Container operation (lightweight images like `FROM scratch`) prioritizes Linux. Distributed artifacts are prebuilt, empty (no-data) binaries for each OS/architecture. |
| **Internal DB engine** | `modernc.org/sqlite` (a pure-Go transpiled SQLite) | No CGO required, easy cross-compilation, conforms to the standard `database/sql` interface. Guarantees full SQL compatibility — views, indexes, triggers, transactions included — at low cost, via a proven implementation. |
| **External I/F** | PostgreSQL-compatible wire protocol (subset) | See §8 for details. Rides directly on existing driver assets like JDBC/psycopg/node-postgres. |

### Binary embedding scheme (fixed-length footer)

Embedding data at the end of the executable uses a **fixed-length footer (trailer)** scheme.

```
[Go binary body (engine portion)] [Data blob (DB state)] [Footer (fixed 32 bytes)]
```

| Field | Size | Content |
| :--- | :--- | :--- |
| Magic | 8 bytes | Identifier (e.g. `"EXECDB01"`) |
| Version | 4 bytes | Format version (**big-endian** unsigned integer) |
| DataOffset | 8 bytes | The data blob's start offset (absolute position from the start of the file, **big-endian**) |
| DataLength | 8 bytes | The data blob's length (**big-endian**) |
| Reserved | 4 bytes | For future extension |

Every integer field in the footer is encoded **big-endian**.

* **Reading:** get its own path via `os.Executable()`, then `Seek` to read the last 32 bytes of the file — that's enough to locate the footer. No ELF/Mach-O section-header parsing needed.
* **Writing (on `.snapshot`, saving under a different name):** at startup, only the offset range of its own engine portion (0 to DataOffset, or the whole file size if there's no trailing footer) is kept in memory, and the actual engine byte sequence is re-read right before saving (a binary bundling `modernc.org/sqlite` runs 10-15MB-class, so the byte sequence itself is not kept resident). At save time, a new snapshot is generated from "engine bytes + new data + new footer." No diffing or relinking is needed. Writing uses the atomic pattern of writing to a temp file in the same directory and then `rename`ing it, so a reader never observes a partially-written, incomplete file. Also, since acquiring the data blob (`Serialize`, below) is done only after guaranteeing that no other session has a write transaction in flight, the snapshot is always consistent even if writes are happening concurrently.
* **Writing (on `.overwrite`, self-overwrite):** writing directly over the same file (itself) while it's running is rejected at the OS level by the write lock on the running file (Linux's `ETXTBSY`, Windows' `ERROR_SHARING_VIOLATION`). To work around this, the following steps are taken (proof-of-concept verified on both Linux and Windows).
  1. `rename` the currently running self from `<path>` to `<path>.execdb_old` to stage it aside (renaming a file that's currently running is allowed on both Linux and Windows)
  2. write the new content (engine bytes + new data + new footer) fresh, into the now-vacant original path
  3. attempt to delete the staged file (succeeds immediately on Linux; on Windows it's expected to fail since it's locked while running — deleted on a best-effort basis at the next startup instead)

  If the write in step 2 fails, the file staged aside in step 1 is moved back to the original path on a best-effort basis (i.e. an attempt is made to restore the state from before the failure). Also, if the running process is a temporary binary produced by `go run` (since the OS deletes the file itself the moment the process exits, overwriting it would be pointless), `.overwrite` rejects the operation with an explicit error.
* **A freshly distributed, data-free binary:** if no magic bytes are found at the end of the file, it starts up as "engine only, empty data." A plain binary straight out of `go build` is directly runnable as-is, and only becomes a binary with data once `.snapshot` or `.overwrite` is run.
* This scheme is a well-proven technique used by self-extracting installers and JAR files (searching backward from the end for a ZIP central directory), and it has no effect on how the OS loader executes the file.

### Data blob serialization scheme (finalized)

For turning the "data blob" — the in-memory SQLite DB's state — into a byte sequence, **the scheme adopted is SQLite's own standard `Serialize`/`Deserialize` API (`sqlite3_serialize()` / `sqlite3_deserialize()`)** (finalized 2026-09-04, empirically verified during phase 1 Step 1 — judged not to need a fallback. See the "facts finalized" sections for phases 1/2 in PLAN.md for the detailed verification record). No home-grown serialization format was invented.

* **The actual API:** what `modernc.org/sqlite` (confirmed at v1.58.0) provides is
  `func (c *conn) Serialize() ([]byte, error)` and
  `func (c *conn) Deserialize(buf []byte) error` — **there is no argument for
  passing a schema name** (it always targets the main schema; this is not the
  originally assumed shape of `Serialize("main")` /
  `Deserialize("main", data)`). The `conn` type itself is unexported, but its
  methods are exported, so it can be reached from `database/sql`'s `*sql.Conn`
  by calling `conn.Raw(func(driverConn any) error { ... })` and type-asserting
  `driverConn` against a locally defined interface (e.g.
  `interface{ Serialize() ([]byte, error) }`).
* **On write (`.snapshot` / `.overwrite`):** the return value of `Serialize()`,
  obtained via the method above, is written as-is as the "data blob" in the
  footer scheme.
* **On startup:** the data blob read from the end of the binary is passed
  as-is to `Deserialize(data)`, obtained the same way, causing SQLite to
  internally re-expand it as an in-memory DB. Because `modernc.org/sqlite`
  internally calls `sqlite3_deserialize()` specifying
  `SQLITE_DESERIALIZE_RESIZEABLE|SQLITE_DESERIALIZE_FREEONCLOSE`, **the
  restored DB is expandable just like a normal DB** (large numbers of INSERTs
  are possible — empirically confirmed it does not become a fixed-size,
  restore-only DB).
* **Connection-sharing model (`memdb` VFS):** the DSN adopted is
  `file:/<name>?vfs=memdb&_busy_timeout=<N>` (the `memdb` VFS implemented by
  `modernc.org/sqlite`). A DSN whose name starts with `/` is shared by
  multiple connections within the same process, against the same in-memory
  store. Because `memdb`'s locking is implemented with the same kind of
  SHARED/RESERVED/EXCLUSIVE mechanism as a file-based DB, `busy_timeout`
  (SQLite's standard busy-handler mechanism) correctly kicks in on a lock
  conflict between connections, waiting a bounded amount of time before
  giving up (see "Concurrency control" in chapter 2). The originally
  considered `file:<name>?mode=memory&cache=shared` (shared-cache) was
  rejected, since empirical testing showed that a lock conflict produces a
  separate class of error (`SQLITE_LOCKED_SHAREDCACHE`) for which
  `busy_timeout` doesn't apply, and the wait itself can't even be cancelled
  from the application side via `context` — it can hang indefinitely. There's
  still one "keeper connection" held open until `Close()`, so the store isn't
  released even if every other connection closes (the keeper exists solely to
  keep the store alive — it's never used to execute SQL statements itself).
* **The relationship between `Deserialize` and multiple connections:**
  `Deserialize` is implemented in a way that internally reopens the schema as
  an anonymous (unnamed) `memdb` store — the result is only reflected on the
  connection that called it, and is not visible even from another connection
  opened later with the same DSN. Because of this, `Open`/`OpenSelf`/`Load`
  take a two-step approach: first `Deserialize` into a throwaway connection,
  then copy that content into the "live" DB (the store the keeper connection
  holds) using SQLite's standard online Backup API
  (the `sqlite3_backup_*` family — exposed by `modernc.org/sqlite` as
  `NewBackup`/`NewRestore`). Copying via the Backup API is SQLite's genuine
  B-tree/pager-level replication, and unlike `Deserialize`, the result is
  visible from every connection on the destination side (including ones
  already open before the Backup ran). Because `Load` takes this approach —
  overwriting the "live" DB in place — other sessions already open before
  `.load` runs (chapter 4) can see the new data without losing their
  connection.
* **Known limitation:** SQLite's own `Serialize`/`Deserialize` cannot handle
  non-contiguous byte sequences, which in theory caps the database size at
  under 2GB (since SQLite never allocates more than 2GB of memory at once).
  However, the `memdb` VFS that was adopted has a separate default cap of its
  own, `SQLITE_MEMDB_DEFAULT_MAXSIZE` (1GiB), and empirical testing confirmed
  it hits `SQLITE_FULL` around ~960MiB — this ~1GiB figure is the effective
  limit. This shouldn't normally be an issue for ExecDB's primary use cases
  (CI/CD test DBs, mock APIs, learning sandboxes, etc.), but it could be a
  constraint for use cases handling large volumes of data.

---

## 8. External I/F protocol specification (PostgreSQL-compatible wire protocol)

Rather than defining a brand-new protocol, the external I/F implements a **subset of the PostgreSQL wire protocol (v3)**. This lets already-established PostgreSQL driver assets across languages/interfaces — JDBC (pgJDBC), Python (psycopg), Node.js (node-postgres), .NET (Npgsql), Go (pgx), ODBC (psqlODBC), PHP (PDO_PGSQL), Ruby (the `pg` gem), Rust (postgres/tokio-postgres) — connect as-is with no ExecDB-specific work. This is the same strategy adopted by emerging distributed databases like CockroachDB and YugabyteDB. **All nine of pgx, pgJDBC, psycopg, node-postgres, Npgsql, psqlODBC, PDO_PGSQL, the `pg` gem, and Rust have been empirically confirmed to connect and run SELECT/DML/transactions and DDL rejection (phase 4 Steps 5 and 7, further verification added after phase 4 completion, `tests/pgclient`/`tests/drivers/`). Npgsql is the sole exception where each driver's own "default connection settings" assumption breaks down — its connection string needs `Server Compatibility Mode=NoTypeLoading` specified (see below for details). The other 8 connect with no such flag needed. psqlODBC needs no client-side flag, but to get `SQLTables`/`SQLColumns` (the ODBC standard API that returns table/column lists, used by Excel/Power BI/Access, etc.) working too, ExecDB's server side adds views/functions compatible with the Postgres system catalog (`pg_type`/`pg_class`/`pg_namespace`/`pg_attribute`, etc. — `cmd/execdb/pgcatalog.go`, see `.claude/rules/pgwire.md` for details). Rust's `postgres`/`tokio-postgres` crate independently reimplements the wire protocol, and testing it uncovered and fixed a latent ExecDB bug (a mismatch between the column OID reported by `Describe` versus `Execute`) — a fix that benefits every other driver too (see `.claude/rules/pgwire.md` for details).**

### Adopted scope (subset)

Rather than implementing the full feature set of the Postgres protocol, ExecDB narrows it down to the minimal message flow needed for connecting and executing queries.

| Implemented | Not implemented (out of initial scope) |
| :--- | :--- |
| Authentication handshake (`trust`-equivalent by default; cleartext-password auth only when `--user` is given — see §9) | SCRAM/MD5 and other auth methods |
| Simple Query protocol (`Query` message) | `COPY`-family protocol, LISTEN/NOTIFY |
| Extended Query protocol (`Parse`/`Bind`/`Describe`/`Execute`/`Sync`/`Close`/`Flush` — switched to adopted in phase 4 Step 5, reasons below) | Binary format for NUMERIC (not implemented, due to complexity — reasons below) |
| Binary format for both result values and parameter values (`int2`/`int4`/`int8`/`float4`/`float8`/`bool`/`bytea`/`timestamp` — reasons below) | — |
| `RowDescription` / `DataRow` / `CommandComplete` | Row-count-limited execution, `PortalSuspended` (`Execute`'s maxRows is ignored — it always runs to completion) |
| Error responses (`ErrorResponse`) | The full detailed SQLSTATE code taxonomy (a simplified set of codes stands in) |

**Why Extended Query was switched to adopted (discovered during the phase 4 Step 1 spike):**
With Simple Query only, neither `pgx` (without `default_query_exec_mode=simple_protocol`) nor pgJDBC (without `preferQueryMode=simple`) could connect without an explicit override, and **Npgsql always uses Extended Query, so it couldn't connect at all** — this contradicted the very goal this section opens with, that "existing driver assets connect as-is with no ExecDB-specific work," so Extended Query, originally out of scope, was switched to adopted (see `.claude/rules/pgwire.md`).

Since SQLite can natively interpret `$1`-style placeholders (empirically
confirmed), no SQL rewriting layer is needed. The result-column information
that `Describe` (statement) requires ahead of execution is obtained by
reading `ColumnTypes()` from a trial run (wrapped in SAVEPOINT/ROLLBACK) with
every placeholder trial-bound to NULL — no new API needed to be added to
`engine`. **`Describe` (portal, after Bind) uses that portal's actual Bind
values for the trial binding, instead of NULL** — for an expression like
`SELECT $1`, where the placeholder itself becomes a result column, using the
real value lets `columnOID`'s ScanType fallback detect the correct type
(real PostgreSQL itself also determines this kind of column's type from the
parameter type the client declared via `Parse`). Originally, both
statement- and portal-level Describe trial-bound NULL uniformly; this
surfaced during phase 4 Step 7 verification with Npgsql, when a `SELECT $1`
column fell back to OID 25 (text), causing `ExecuteScalarAsync()`'s strict
type cast to fail (pgJDBC/node-postgres hadn't surfaced this, since their
value-retrieval APIs implicitly convert strings).

**Why Npgsql couldn't even connect at first, and its root cause (found during
phase 4 Step 7 real-world verification):** adopting Extended Query (Step 5)
was supposed to resolve `unsupported message type 'P'`, but actually
connecting Npgsql with default settings failed with
`SQL logic error: no such function: version`. The cause: Npgsql's own
connection-establishment process independently performs "type catalog
bootstrapping" — right after `SELECT version();`, it batch-sends multiple
SELECTs, as a single Simple Query message, against real Postgres system
catalogs — `pg_type`/`pg_namespace`/`pg_class`/`pg_proc`/`pg_range`/
`pg_attribute`/`pg_enum`. Since SQLite has none of these functions/tables at
all, the very first statement at connection time fails. None of
pgx/psycopg/node-postgres/pgJDBC perform this kind of bootstrapping (it
doesn't happen with default settings), so this problem was specific to
Npgsql. **Fix: specify `Server Compatibility Mode=NoTypeLoading` in the
connection string** (a standard feature Npgsql itself officially documents
for connecting to compatible-but-not-genuine-Postgres backends like
CockroachDB/Redshift — not an ExecDB-specific patch). With this specified,
Npgsql skips the type-catalog bootstrapping entirely and operates using only
its built-in known types (int4/text/bool, etc.), which fundamentally avoids
this problem. The caller of `tests/drivers/dotnet/run.sh`
(`tests/drivers/run-all.sh`) supplies this connection-string parameter.

**The pg_catalog-compatible views added for psqlODBC (ODBC) support (added
after phase 4 completion, found during real-world verification):** right
after connecting, psqlODBC queries the real Postgres system catalog
`pg_type` for the presence of the large-object type, and
`SQLTables`/`SQLColumns` (the ODBC standard APIs — used by Excel/Power
BI/Access, etc. — that return table/column lists) send full-blown JOIN
queries against `pg_class`/`pg_namespace`/`pg_attribute`/`pg_attrdef` plus
calls to the `pg_get_expr()`/`current_schema()` functions. Since SQLite has
none of these at all, neither connecting nor schema-browsing works as-is.
**Fix: provide `TEMP` views/tables, per pgwire connection, dynamically
derived from ExecDB's real schema** (`sqlite_master`/
`pragma_table_info()`) (`cmd/execdb/pgcatalog.go`). This never pollutes real
data at all (it doesn't show up in `.tables`/`.dump`/snapshots), and always
reflects the latest state even after schema changes. Schema-qualified
references the client sends, like `pg_catalog.pg_class`, are absorbed by
stripping the `pg_catalog.` string server-side before executing (SQLite's
VIEWs can't reference objects in another database — an attempt to put
`main`-referencing views in an independent schema via
`ATTACH DATABASE ... AS pg_catalog` was rejected for that reason; see
`.claude/rules/pgwire.md` for the full story). `current_schema()`/
`pg_get_expr()` are registered via `engine.RegisterScalarFunction` (newly
added, a thin wrapper around `modernc.org/sqlite`'s custom-function
registration API). Constraints, indexes, triggers, and multiple schemas are
out of scope — the goal is for basic connections, typed queries, and
schema browsing to work against the real schema, not a full-fidelity
reproduction of the Postgres system catalog.

**Why a binary format for result values became necessary (found during
phase 4 Step 5 real-world verification):** implementing text format only,
`pgx`'s default (Extended Query) connection failed across the board for
`int8`/`float8`/`numeric`/`bool`/`bytea`/`timestamp` columns (numeric/
datetime types failed with an explicit error; `bool`/`bytea` were more
dangerous, **silently returning a wrong value with no error at all**). The
cause: `pgx` requests binary format by default for these types via the
`resultFormatCodes` in its `Bind` message — meaning that without ExecDB
implementing binary result values, Extended Query's whole point (connecting
with default settings) couldn't be achieved for any realistic query
involving numeric, BLOB, or datetime columns. NUMERIC alone was excluded —
since SQLite's NUMERIC affinity is always internally stored as either
INTEGER or REAL (it has no true arbitrary-precision decimal), rather than
implementing Postgres's complex binary NUMERIC format (base-10000
digit-group encoding), NUMERIC-affinity declared types are routed to
`int8`/`float8` based on the actual runtime Go dynamic type
(int64/float64) instead (see `cmd/execdb/pgtype.go` for details).

**Why a binary format for parameters became necessary (found during phase
4 Step 7 real-world verification):** as of Step 5, parameters were assumed
to always be text format, and `Bind` uniformly rejected binary-format
parameters (this hadn't surfaced a problem yet, since `pgx`/`psycopg` send
parameters as text by default). But during phase 4 Step 7's other-language
driver verification, it turned out that pgJDBC, using
`PreparedStatement.setInt`/`setDouble`, etc. with default settings, sends
`Bind` message parameters in binary format (the `Parse` message itself
self-declares the type — e.g. type OID 23/`int4` for `setInt` — regardless
of how `ParameterDescription` answers; the client hardcodes based on the
type it itself knows). This meant that DML/SELECT via prepared statements
failed across the board under pgJDBC's default settings — directly
contradicting the whole point of adopting Extended Query — so `Bind` was
changed to also decode binary-format parameters. Decoding relies on the
parameter type OID the `Parse` message itself sent as the client's own
declaration (previously discarded) — since the `Bind` wire format itself
carries no type information at all, that's the only clue available. The
supported OID set is wider than for result values — 8 types including
`int2`/`int4`/`float4` (OIDs `columnOID` never uses on the result-value
side, but which genuinely appear as client-declared parameter types — see
`cmd/execdb/pgtype.go`'s `decodeBinaryParam`). If a client sends binary
format without declaring a type OID (0/unspecified), there's no way to
decode it, so it's rejected as before.

### Authentication (opt-in, Zero-Auth by default)

While ExecDB holds "Zero-Auth" from chapter 1 as a core philosophy, it provides an opt-in authentication mechanism so that anyone who needs it can turn it on easily.

* **Default (`--user` unspecified):** always Zero-Auth. `trust`-equivalent as before — no password needed, anyone can connect.
* **How the password is obtained when `--user NAME` is specified:** regardless of mode, decided in this priority order.
  1. **If the `EXECDB_PASSWORD` environment variable is set:** use its value as the password, skipping the interactive prompt (this takes top priority whenever set, in either REPL mode or server mode).
  2. **`EXECDB_PASSWORD` unset, and in REPL mode:** prompt interactively for the password from stdin at startup (a masked-input `Password: `-style prompt, equivalent to `golang.org/x/term`'s `ReadPassword`).
  3. **`EXECDB_PASSWORD` unset, and `--no-repl` (server mode):** there's no one to prompt, so **startup is aborted with an error**.
* **There's no concept of "a user":** no multi-user management or permission separation — it's treated simply as one name+password pair, a single authentication key required to connect.
* **Auth method:** adopts the PostgreSQL wire protocol's `cleartext password` authentication (full mechanisms like `SCRAM`/`MD5` are not implemented — see "Adopted scope" in §8).

### Type mapping (finalized, phase 4 Steps 1-2)

Because SQLite is dynamically typed (type affinity), a column's value type isn't fixed. The Postgres type OID returned in the `RowDescription` message prioritizes the column's declared type (`decltype`), routed per SQLite's own official type-affinity algorithm (sqlite.org/datatype3.html §3.1) as follows. `BOOLEAN` and `DATE`/`DATETIME`/`TIME` aren't categories in SQLite's standard five-way classification, but since `modernc.org/sqlite` gives them observable special treatment (`BOOLEAN` columns are stored as integers; `DATE`-family columns come back from `Scan` as `time.Time`), they're checked before the standard five-way classification.

| Substring present in the declared type (decltype) | Postgres OID | Notes |
| :--- | :--- | :--- |
| `BOOL` | `bool`(16) | Checked before the standard five-way classification |
| `DATE` or `TIME` | `timestamp`(1114) | Same as above |
| `INT` | `int8`(20) | Standard classification "INTEGER" |
| `CHAR` / `CLOB` / `TEXT` | `text`(25) | Standard classification "TEXT" |
| `BLOB` | `bytea`(17) | Standard classification "BLOB" |
| `REAL` / `FLOA` / `DOUB` | `float8`(701) | Standard classification "REAL" |
| Matches none of the above (the standard classification "NUMERIC" catch-all, e.g. `NUMERIC`/`DECIMAL(10,2)`) | Falls through to the fallback below | No fixed OID assigned (reasons below) |
| No declared type (expression/aggregate/literal column) | Falls through to the fallback below | Same as above |

**Fallback (sampling the actual runtime Go dynamic type):** columns not
settled by the table above are routed by the actual Go type of `ScanType()`
obtained by trial-running the first row
(`int64`→`int8`, `float64`→`float8`, `bool`→`bool`, `[]byte`→`bytea`,
`time.Time`→`timestamp`, anything else or no rows→`text`). **Routing
NUMERIC-affinity declared types through this path is a deliberate design
decision** — since SQLite's NUMERIC affinity is always internally stored as
either INTEGER or REAL, with no true arbitrary-precision decimal, routing
it to `int8`/`float8` avoids the need to implement Postgres's complex
binary NUMERIC format (base-10000 digit-group encoding) at all.

Text/binary encoding of values is decided by the actual Go dynamic type
after `Scan`, not by the OID (since the declared type and the actual value's
type can disagree — a value violating type affinity may be stored). See
`columnOID`/`affinityOID`/`scanTypeOID`/`pgEncodeValue` in
`cmd/execdb/pgtype.go` for the implementation details.

**OIDs supported for binary-format decoding on the parameter (`Bind`)
side:** 8 types — `int2`(21)/`int4`(23)/`int8`(20)/`float4`(700)/
`float8`(701)/`bool`(16)/`bytea`(17)/`timestamp`(1114) (`int2`/`int4`/
`float4` are OIDs `columnOID` never returns on the result-value side, but
they independently appear as client-self-declared parameter types — see
"Why a binary format for parameters became necessary" above for details).

### Integration with access control (§2)

DDL continues to be rejected over the external I/F. On the Postgres protocol, this comes back as an `ErrorResponse` message.

```
ErrorResponse
  Severity: ERROR
  Code:     42501  (borrowing the SQLSTATE for insufficient_privilege)
  Message:  "DDL statements are not allowed via external interface"
```

`SET`/`SHOW` are treated as a third category that doesn't belong to this "allow/reject" dichotomy (see "`SET`/`SHOW` and other session commands" in §2). The external-I/F layer intercepts them before they reach the internal SQL engine and just keeps the value in an in-memory, per-connection map (`CommandComplete("SET")` / a one-column, one-row result for `SHOW`) — no `ErrorResponse` is returned.

### Transaction (TCL) handling

Since a Postgres protocol connection is itself stateful, the "session ID" approach needed for a connectionless scheme like HTTP is unnecessary. ExecDB handles `BEGIN` through `COMMIT`/`ROLLBACK` directly per TCP connection, following Postgres's own native session management as-is.

**Implementation:** one TCP/UNIX Domain Socket connection corresponds to one dedicated connection returned by `engine`'s `DB.Session(ctx)` (see chapter 6). `BEGIN`/`COMMIT`/`ROLLBACK` are sent to that connection as plain SQL statements, and SQLite itself treats them as a real transaction. ExecDB doesn't reimplement transaction state on its own side, but the `ReadyForQuery` message's status byte does reflect the actual state: `'I'` (idle) / `'T'` (in a transaction) / `'E'` (an error occurred inside a transaction, and only `COMMIT`/`ROLLBACK` are accepted from here on). Sending anything other than `COMMIT`/`ROLLBACK` while in state `'E'` is rejected — without executing it — with an `ErrorResponse` (SQLSTATE `25P02`, "current transaction is aborted") — a half-baked implementation that only shows `'E'` in the status while actually still executing the statement would confuse drivers (pgx/JDBC, etc.) that strictly track transaction state, so that approach is not adopted.

### Query cancellation (`CancelRequest` / `BackendKeyData`, phase 4 Step 6)

There are deliberately two separate paths for query cancellation.

1. **Automatic cancellation on client disconnect** (since phase 2 Step 5): a
   separate goroutine watches for reads on the connection while a query is
   executing, and cancels the running query as soon as it detects the
   client disconnecting (or sending unexpected data).
2. **The `CancelRequest` protocol** (an explicit cancel request from another
   connection): at connection establishment, the server sends the client a
   pseudo-PID/secret pair via the `BackendKeyData` message. The client opens
   **a separate, new connection** and sends a `CancelRequest` carrying that
   PID/secret, which interrupts only the query currently running on the
   target connection (the connection itself is not disconnected, and can
   keep being used afterward). If the PID/secret doesn't match, or the
   target is idle (no query running), it's a silent no-op — matching real
   PostgreSQL.

Both share the same cancellation mechanism at the implementation level
(`cmd/execdb/pgcancel.go`). See `.claude/rules/pgwire.md` for details and a
design pitfall discovered during implementation (around reusing a
`context.CancelFunc`).

### Transport

| Transport | Notes |
| :--- | :--- |
| TCP | The standard connection method. Various drivers like JDBC use this by default |
| UNIX Domain Socket | Postgres itself has a convention of using a Unix socket for local connections, and some drivers like psycopg support this too. An option aimed at local use |

---

## 9. Startup options

Command-line arguments at startup provide both a short flag (one character) and a long flag. Short flags are kept **entirely lowercase**, to avoid the mnemonic difficulty of mixed case.

| Short | Long form | Type | Default | Role |
| :--- | :--- | :--- | :--- | :--- |
| `-p` | `--pg-addr` | string | `""` (disabled) | TCP listen address for the PostgreSQL-compatible wire protocol (e.g. `:5432`, `127.0.0.1:5432`). If unspecified, the external I/F is not started |
| `-s` | `--socket` | string | `""` (disabled) | Path for also listening over the same protocol via a UNIX Domain Socket (e.g. `/tmp/execdb.sock`). If unspecified, the Socket I/F is not started |
| `-u` | `--user` | string | `""` (disabled, Zero-Auth) | When specified, requires an authentication key (name+password) for external-I/F connections (see §8). The password uses the `EXECDB_PASSWORD` environment variable if set; otherwise, only in REPL mode, it's prompted for interactively at startup (an error if `--no-repl` and unset) |
| `-o` | `--snapshot-as` | string | (unspecified) | The default filename used by `.snapshot`, and by the auto-save on `--no-repl` (server mode) shutdown (see below) |
| `-n` | `--no-repl` | bool | `false` | Runs headless in the background with only the external I/F, without starting the REPL (server mode) |
| `-q` | `--quiet` | bool | `false` | Suppresses the startup banner/log output |
| `-t` | `--timestamp` | bool | `false` | Whether to append a timestamp to the filename on save (see below) |
| `-i` | `--snapshot-interval` | duration | `0` (disabled) | Automatically saves a separate-file snapshot at this interval (e.g. `5m`, `1h`). Works in either REPL mode or server mode |
| `-h` | `--help` | bool | - | Show help |

Both `--pg-addr` and `--socket` are optional and can be used together. If both are omitted, it starts as a standalone REPL (no external I/F).

### Timestamping

Whether a `_YYYYMMDDHHMMSS` date/time timestamp is appended to the filename on `.snapshot` can be toggled with `--timestamp` (`-t`). It's a **bool flag** with no choice of value.

* **When `--timestamp` is given:** the filename is generated by the following rule (a single, shared rule regardless of whether the filename is omitted or given explicitly, and regardless of whether it comes from a CLI startup option or the REPL's `.snapshot` command).
  1. If the base filename (excluding the extension) already contains a `_YYYYMMDDHHMMSS` pattern, it's stripped first (to avoid double-appending).
  2. A new `_YYYYMMDDHHMMSS` is inserted right before the extension.
  3. On Windows, if the extension is omitted, `.exe` is appended.

  | Base filename | Result |
  | :--- | :--- |
  | `mydb` (filename omitted; the running binary's name is the base) | `mydb_YYYYMMDDHHMMSS` |
  | `mydb_20260101120000` (already has a timestamp) | `mydb_YYYYMMDDHHMMSS` (the old one is stripped and replaced) |
  | `mydb.exe` | `mydb_YYYYMMDDHHMMSS.exe` |
  | `mydb_20260101120000.exe` | `mydb_YYYYMMDDHHMMSS.exe` |
  | `mydb` (given explicitly via `-o mydb`, on Windows) | `mydb_YYYYMMDDHHMMSS.exe` (`.exe` is appended when the extension is omitted) |

* **When `--timestamp` is not given (the default):** no timestamp is appended — the given filename (or, on Windows, the filename with the `.exe` extension filled in) is used as-is. Overwriting a same-named file can happen.

  | Base filename | Result |
  | :--- | :--- |
  | `mydb` | `mydb` (Linux/macOS) / `mydb.exe` (Windows) |
  | `mydb.exe` | `mydb.exe` |

This option acts as the default value at startup, but the same option can also be given on the spot when running the REPL's `.snapshot` command, overriding it.

```
.snapshot bug_123 --timestamp
```

`.overwrite` has no concept of specifying a filename (it's fixed to its own path), so the `--timestamp` option doesn't apply to it.

### Server mode (`--no-repl`) shutdown and saving

When started with `--no-repl`, there's no REPL, and thus no way to type a `.snapshot`/`.overwrite` command. Instead, ExecDB takes the approach of **automatically saving a snapshot on receipt of a termination signal, then exiting**.

* **How it saves:** on receiving `SIGTERM`/`SIGINT`, it auto-saves (as a separate file) per the `--snapshot-as`/`--timestamp` settings, then exits the process.
* **How this differs from REPL mode's policy:** the principle stated in §1/§4 — "save only via an explicit action, no auto-save" — is **a REPL-mode policy**. In server mode, since there's no way to type `.snapshot` at all, the termination signal itself is treated as "the trigger for a save." This difference is treated as an exception specific to server mode.
* **Platform-specific support status:**

  | OS | Support status |
  | :--- | :--- |
  | Linux / macOS | Supported via standard `SIGTERM`/`SIGINT` handling (including shutdown requests from `docker stop`, systemd, etc.) |
  | Windows (attached to a console) | Supported, since console control events like Ctrl+C are converted to `SIGINT`-equivalent signals via Go's `os/signal` |
  | Windows (running headless as a Windows Service) | **Out of scope for v1 (a known limitation).** Since there's no console, control events never arrive, and a proper auto-save-and-exit isn't guaranteed. If needed in the future, Service Control Manager integration via `golang.org/x/sys/windows/svc` would be considered separately |

### Periodic snapshots (`--snapshot-interval`)

Given `--snapshot-interval` (`-i`), a separate-file snapshot save is automatically repeated at that interval. Works in either REPL mode or server mode.

* **Purpose:** in REPL mode, this serves as a safety net against the accident of "forgetting to save during a long interactive session and losing everything to a crash"; in server mode, it works as a periodic backup. It's positioned as an opt-in exception to the "save only via an explicit action" principle from §1.
* **Save method:** only separate-file saves (`.snapshot`-equivalent) are supported. Periodic self-overwrite (`.overwrite`-equivalent) is not supported, due to the high risk from frequently rewriting the file currently running.
* **Filename:** follows the `--snapshot-as`/`--timestamp` settings. If `--timestamp` is given, a new timestamped file keeps being generated at every interval, so file growth/cleanup is, as in §4, left to operational (user) judgment. To keep overwriting a fixed filename instead, don't specify `--timestamp` (the default).

---

## 10. Lifecycle and operational flow

```text
[1. Startup]
   └─> Run ./execdb
   └─> Read data from the end of the binary and unpack it into memory (RAM)
   └─> (only when `--user` is given) Determine the password (see §8, §9)
        ├─> If the `EXECDB_PASSWORD` environment variable is set, use it (no interactive prompt)
        ├─> Unset, and in REPL mode: prompt interactively for a password from stdin
        └─> Unset, and `--no-repl` (server mode): abort startup with an error
   └─> Start the external I/F (PostgreSQL-compatible wire protocol) in the background
   └─> Show the startup banner (see below; suppressible with `-q`/`--quiet`)
   └─> Start the "interactive console (REPL)" in the foreground (with `--no-repl`, transition to server mode here instead)

[2. Operation & query execution]
   ├─> Interactive console: can run all of DDL / DML / TCL
   └─> External I/F      : accepts only DML / TCL (DDL is rejected with an ErrorResponse)

[3. Save (separate file)]
   └─> Run the .snapshot command (only via an explicit action, no auto-save)
   └─> Pack the latest in-memory state together with the engine itself
   └─> Generate and output a new, differently-named executable (e.g. execdb_20260831_150000)

[3'. Save (self-overwrite)]
   └─> Run the .overwrite command (only via an explicit action)
   └─> Stage itself aside to <path>.execdb_old → write the new content into the now-vacant original path (§7)
   └─> On success, exit the REPL right there (this is the only operation where saving and exiting happen together; see §4)

[4. Shutdown (REPL mode)]
   └─> Normal exit paths: the .exit / .quit commands, or EOF on stdin (Ctrl+D)
   └─> Ctrl+C (SIGINT) on an interactive terminal is treated not as an exit but as an interrupt command (following the sqlite3 shell's convention):
        ├─> Pressed while a query is executing: interrupts just that query and returns to the prompt (the process doesn't exit)
        ├─> Pressed once while idle (no query running): discards an in-progress, unconfirmed multi-line SQL statement and just reprints the prompt (the process doesn't exit)
        ├─> Pressed twice in a row while idle with no new input line typed in between: exits the process at that point
        │    (exit code 1; reading even a single new input line resets the consecutive-press count)
        └─> If stdin is not an interactive terminal (piped/scripted execution), this handler is never registered at all,
             and SIGINT is treated as ordinary process termination (to avoid the accident of an ExecDB launched from a
             script becoming impossible to stop with Ctrl+C)
   └─> On receiving SIGTERM (not subject to auto-save in interactive mode; see [4'.] for the difference from server mode),
        the process exits
   └─> Regardless of the exit path, in-memory data is not saved and is lost (volatility is assumed as a given —
        run .snapshot or .overwrite before exiting if you want to save it)

[4'. Shutdown (server mode, --no-repl)]
   └─> Receives SIGTERM / SIGINT (not applicable when running headless on Windows; see §9)
   └─> Automatically saves a snapshot (separate file) per the --snapshot-as / --timestamp settings
   └─> Exits the process once the save completes
```

### Startup banner

At startup, a banner is shown indicating that the REPL/external I/F are now available. It shows: the version, whether there's embedded data and, if so, the snapshot's name, the external I/F's listening status (only if configured), whether `--user` authentication is enabled (the password itself is never shown), and whether it's in server mode. Rather than a dedicated command like `.status`, this is consolidated into presenting it once, via this banner, at startup. `-q`/`--quiet` suppresses the entire banner.

**REPL mode, external I/F enabled:**
```
ExecDB v0.1.1
Loaded snapshot: mydb_20260901120000
Listening on :5432 (PostgreSQL wire protocol)
Listening on /tmp/execdb.sock (UNIX Domain Socket)
Enter ".help" for usage hints.
```

**REPL mode, no data, no external I/F:**
```
ExecDB v0.1.1
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
```

**Server mode (`--no-repl`):**
```
ExecDB v0.1.1
Loaded snapshot: mydb_20260901120000
Listening on :5432 (PostgreSQL wire protocol)
Running in server mode (--no-repl). Send SIGTERM to save and exit.
```
