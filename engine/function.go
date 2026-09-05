package engine

import (
	"database/sql/driver"

	"modernc.org/sqlite"
)

// FunctionContext is passed to a function registered via
// RegisterScalarFunction. It carries no methods callers currently need;
// it exists so RegisterScalarFunction's signature does not depend on
// modernc.org/sqlite's own type outside this file.
type FunctionContext = sqlite.FunctionContext

// RegisterScalarFunction registers a custom scalar SQL function named
// name, usable from any SQL text executed against any engine.DB opened
// afterward. This wraps modernc.org/sqlite.RegisterDeterministicScalarFunction
// (the top-level, stable driver package -- unlike Complete's
// modernc.org/sqlite/lib, this needs no .claude/rules/sqlite-quirks.md
// upgrade caveat). Registration is process-wide and one-shot per name
// (modernc.org/sqlite errors if the same name is registered twice), not
// per-DB or per-Session, matching the underlying library's own semantics.
//
// This exists for cmd/execdb's pgwire layer to expose a handful of
// PostgreSQL built-in functions (current_schema(), pg_get_expr(), ...)
// that its pg_catalog-compatibility views (phase 4 follow-up: ODBC/
// psqlODBC support) need callable from plain SQL -- engine itself has no
// notion of PostgreSQL; it only provides the mechanism.
func RegisterScalarFunction(name string, nArgs int32, fn func(ctx *FunctionContext, args []driver.Value) (driver.Value, error)) error {
	return sqlite.RegisterDeterministicScalarFunction(name, nArgs, fn)
}
