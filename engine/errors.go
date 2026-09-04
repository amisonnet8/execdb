package engine

import "errors"

// ErrNotOverwritable is returned by Overwrite when the running process's
// own executable path looks like a `go run` temporary binary: `go run`
// deletes it as soon as the process exits, which would make Overwrite a
// no-op that looks like it succeeded.
var ErrNotOverwritable = errors.New("engine: running executable is not overwritable (looks like a `go run` temporary binary)")

// ErrClosed is returned by DB methods called after Close. It is not
// returned by QueryRow: *sql.Row has no way to carry a caller-supplied
// error before Scan is called, so a post-Close QueryRow instead surfaces
// database/sql's own "sql: database is closed" through Scan.
var ErrClosed = errors.New("engine: database is closed")

// ErrNoData is returned by Load when path holds no ExecDB data (spec §4).
var ErrNoData = errors.New("engine: no ExecDB data found")

// ErrTooLarge is returned when the in-memory database cannot be
// serialized as a single contiguous byte slice -- either because it
// exceeds what Serialize can represent, or because modernc.org/sqlite
// returned an empty buffer for a large database rather than an error
// (see .claude/rules/sqlite-quirks.md).
var ErrTooLarge = errors.New("engine: database is too large to serialize")

// ErrBusy is returned when a Snapshot/Overwrite/Load could not acquire
// the serialization barrier because another session held a conflicting
// write transaction open past the live database's busy_timeout.
var ErrBusy = errors.New("engine: database is busy (another session is writing)")
