package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	"github.com/amisonnet8/execdb/engine"
)

// init registers the handful of PostgreSQL built-in scalar functions that
// setupPGCatalog's views call. modernc.org/sqlite's function registration
// is process-wide and one-shot per name (engine.RegisterScalarFunction),
// so this belongs in init(), not per-connection setup.
func init() {
	// current_schema(): psqlODBC's SQLTables/SQLColumns queries call this
	// to resolve the caller's default schema. ExecDB has exactly one
	// (synthetic) schema, "public" (see setupPGCatalog's pg_namespace).
	if err := engine.RegisterScalarFunction("current_schema", 0, func(_ *engine.FunctionContext, _ []driver.Value) (driver.Value, error) {
		return "public", nil
	}); err != nil {
		panic(err)
	}
	// pg_get_expr(pg_node_tree, relation_oid): real PostgreSQL decodes a
	// column's default-value expression tree back into SQL text. ExecDB's
	// pg_attrdef view (setupPGCatalog) never has any rows -- it exists
	// only so the LEFT OUTER JOIN psqlODBC's SQLColumns query performs
	// against it type-checks -- so this is only ever called with NULL
	// arguments and always returns NULL (no default-value text) instead
	// of attempting real pg_node_tree decoding.
	if err := engine.RegisterScalarFunction("pg_get_expr", 2, func(_ *engine.FunctionContext, _ []driver.Value) (driver.Value, error) {
		return nil, nil
	}); err != nil {
		panic(err)
	}
}

// rewritePGCatalogQuery strips a "pg_catalog." schema qualifier from
// query. Real ODBC drivers (psqlODBC, confirmed via phase 4 follow-up
// testing) qualify their system-catalog queries this way (e.g.
// "pg_catalog.pg_class"), but setupPGCatalog's compatibility views live
// in ExecDB's temp schema, not a real attached "pg_catalog" database --
// SQLite forbids a VIEW from referencing another database's objects
// (confirmed empirically: "view pg_class cannot reference objects in
// database main"), which rules out attaching a real ":memory:" database
// named pg_catalog and defining the views there. temp views can
// reference main freely, and are resolved for an unqualified name (no
// real user schema has a table literally named pg_class/pg_type/...), so
// stripping the qualifier before execution is enough to make both the
// qualified and unqualified forms resolve to the same views.
func rewritePGCatalogQuery(query string) string {
	if !strings.Contains(query, "pg_catalog.") {
		return query
	}
	return strings.ReplaceAll(query, "pg_catalog.", "")
}

// pgCatalogAlreadyAttached reports whether sess's underlying physical
// connection already has setupPGCatalog's temp views defined.
// engine.DB.Session hands out connections from database/sql's own pool
// (engine/engine.go), so a new pgwire connection's Session can land on a
// physical connection a previous, already-closed pgwire connection set
// up -- temp objects live for the physical connection's lifetime, not
// the logical Session wrapper's, and there is no "about to return to the
// pool" hook to tear them down symmetrically. Without this check,
// setupPGCatalog's CREATE TEMP VIEW/TABLE fails with an "already exists"
// error the second time any physical connection is reused -- discovered via
// psqlODBC testing (phase 4 follow-up) the moment more than one pgwire
// connection was made against a running server.
func pgCatalogAlreadyAttached(ctx context.Context, sess *engine.Session) (bool, error) {
	var name string
	err := sess.QueryRowContext(ctx, "SELECT name FROM sqlite_temp_master WHERE name = 'pg_type'").Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// setupPGCatalog defines a set of temp views on sess that answer the
// PostgreSQL system-catalog queries real ODBC drivers (psqlODBC,
// confirmed via phase 4 follow-up testing) send during a normal
// connection and when a client asks to browse the schema
// (SQLTables/SQLColumns) -- something none of the other 5 verified
// drivers need (they never query pg_catalog at all). It must run once
// per physical connection (temp objects are a per-connection property,
// like engine/session.go's Session itself), not once per process --
// pgCatalogAlreadyAttached makes it a no-op on a reused connection.
//
// These are temp views, not real tables: they never appear in
// `.tables`/`.dump`/a snapshot (ExecDB's own schema introspection only
// looks at the main schema), and are discarded automatically when the
// connection closes. Every view queries "main.sqlite_master"/
// "main.pragma_table_info(...)" with an explicit "main." qualifier for
// clarity, even though a temp view's unqualified references would
// already resolve to main by SQLite's normal search order.
//
// Scope: enough for basic ODBC connectivity, typed queries, and a
// driver's own schema browser (SQLTables/SQLColumns) to work against the
// real, current SQLite schema. It is not a general pg_catalog
// implementation -- constraints, indexes, triggers, and multiple
// schemas/namespaces are all out of scope (ExecDB has exactly one
// implicit schema), matching phase 4's overall "subset, not full
// PostgreSQL" philosophy (.claude/rules/pgwire.md).
func setupPGCatalog(ctx context.Context, sess *engine.Session) error {
	attached, err := pgCatalogAlreadyAttached(ctx, sess)
	if err != nil {
		return fmt.Errorf("setupPGCatalog: %w", err)
	}
	if attached {
		return nil
	}

	const setupSQL = `
CREATE TEMP VIEW pg_namespace AS
SELECT 2200 AS oid, 'public' AS nspname;

CREATE TEMP VIEW pg_class AS
SELECT
	m.rowid AS oid,
	m.name AS relname,
	2200 AS relnamespace,
	CASE m.type WHEN 'view' THEN 'v' ELSE 'r' END AS relkind,
	0 AS relhasrules,
	0 AS relhassubclass
FROM main.sqlite_master AS m
WHERE m.type IN ('table', 'view') AND m.name NOT LIKE 'sqlite_%';

-- A real (temp) table, not a VALUES-derived view: SQLite has no
-- "AS alias(col1, col2, ...)" derived-table column-renaming syntax
-- (confirmed empirically -- "near '(': syntax error"), so a view over a
-- VALUES clause would only expose SQLite's auto-generated "column1",
-- "column2", ... names, not oid/typname/etc.
CREATE TEMP TABLE pg_type (oid INTEGER, typname TEXT, typbasetype INTEGER, typtype TEXT, typtypmod INTEGER);
INSERT INTO pg_type VALUES
	(16, 'bool', 0, 'b', -1),
	(17, 'bytea', 0, 'b', -1),
	(20, 'int8', 0, 'b', -1),
	(21, 'int2', 0, 'b', -1),
	(23, 'int4', 0, 'b', -1),
	(25, 'text', 0, 'b', -1),
	(700, 'float4', 0, 'b', -1),
	(701, 'float8', 0, 'b', -1),
	(1114, 'timestamp', 0, 'b', -1),
	(1700, 'numeric', 0, 'b', -1);

CREATE TEMP VIEW pg_attribute AS
SELECT
	m.rowid AS attrelid,
	p.name AS attname,
	CASE
		WHEN upper(p.type) LIKE '%BOOL%' THEN 16
		WHEN upper(p.type) LIKE '%BLOB%' THEN 17
		WHEN upper(p.type) LIKE '%INT%' THEN 20
		WHEN upper(p.type) LIKE '%CHAR%' OR upper(p.type) LIKE '%CLOB%' OR upper(p.type) LIKE '%TEXT%' THEN 25
		WHEN upper(p.type) LIKE '%REAL%' OR upper(p.type) LIKE '%FLOA%' OR upper(p.type) LIKE '%DOUB%' THEN 701
		ELSE 25
	END AS atttypid,
	p.cid + 1 AS attnum,
	-1 AS attlen,
	-1 AS atttypmod,
	p."notnull" AS attnotnull,
	0 AS attisdropped,
	'' AS attidentity,
	CASE WHEN p."dflt_value" IS NULL THEN 0 ELSE 1 END AS atthasdef
FROM main.sqlite_master AS m, main.pragma_table_info(m.name) AS p
WHERE m.type IN ('table', 'view') AND m.name NOT LIKE 'sqlite_%';

CREATE TEMP VIEW pg_attrdef AS
SELECT 0 AS adrelid, 0 AS adnum, NULL AS adbin
WHERE 0;
`
	if _, err := sess.ExecContext(ctx, setupSQL); err != nil {
		return fmt.Errorf("setupPGCatalog: %w", err)
	}
	return nil
}
