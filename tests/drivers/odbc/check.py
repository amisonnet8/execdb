"""Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
follow-up): psqlODBC (the official PostgreSQL ODBC driver, via pyodbc),
against a running ExecDB instance already seeded with table t(a INTEGER)
(see tests/e2e.sh).

Unlike the other 5 verified drivers, psqlODBC cannot connect with zero
ExecDB-side support: its connection bootstrap queries the real Postgres
system catalog (pg_type) to check for large-object support, and
SQLTables()/SQLColumns() (used here, and by real schema-browsing ODBC
consumers like Excel/Power BI/Access) query pg_class/pg_namespace/
pg_attribute/pg_attrdef plus the pg_get_expr()/current_schema() built-in
functions. ExecDB answers all of these via a small pg_catalog-compatible
set of temp views/functions set up per pgwire connection
(cmd/execdb/pgcatalog.go) -- see that file's doc comments for exactly
what is (and is not) emulated.
"""
import sys

import pyodbc


def main():
    if len(sys.argv) != 2:
        print("usage: check.py <connection-string>", file=sys.stderr)
        sys.exit(2)

    conn = pyodbc.connect(sys.argv[1], autocommit=True)
    try:
        check(conn)
    finally:
        conn.close()
    print("OK")


def check(conn):
    cur = conn.cursor()

    cur.execute("SELECT ?", (1,))
    (one,) = cur.fetchone()
    assert str(one) == "1", f"SELECT ? (1) returned {one!r}"

    cur.execute("SELECT ?", (3.5,))
    (f,) = cur.fetchone()
    assert float(f) == 3.5, f"SELECT ? (3.5) returned {f!r}"

    cur.execute("SELECT ?", ("hello",))
    (s,) = cur.fetchone()
    assert s == "hello", f"SELECT ? ('hello') returned {s!r}"

    cur.execute("SELECT NULL")
    (null,) = cur.fetchone()
    assert null is None, f"SELECT NULL returned {null!r}"

    # spec §2: DDL must be rejected via the external I/F, and pyodbc must
    # surface it as an Error with SQLSTATE 42501 (spec §8).
    try:
        cur.execute("CREATE TABLE odbc_should_not_exist(a INTEGER)")
        raise AssertionError("expected CREATE TABLE to be rejected via the external I/F")
    except pyodbc.Error as e:
        assert e.args[0] == "42501", f"expected SQLSTATE 42501, got {e.args[0]!r} ({e})"

    # A basic write/read round trip against the table tests/e2e.sh seeded.
    cur.execute("INSERT INTO t VALUES (777006)")
    cur.execute("SELECT count(*) FROM t WHERE a = 777006")
    (n,) = cur.fetchone()
    assert n == 1, f"expected count=1 after INSERT, got {n}"

    # cmd/execdb/pgcatalog.go's actual purpose: a driver's own schema
    # browser (SQLTables/SQLColumns) sees ExecDB's real, current schema.
    tables = [row.table_name for row in cur.tables() if row.table_type == "TABLE"]
    assert "t" in tables, f"cursor.tables() did not list 't': {tables!r}"

    columns = {row.column_name: row.type_name for row in cur.columns(table="t")}
    assert "a" in columns, f"cursor.columns(table='t') did not list 'a': {columns!r}"


if __name__ == "__main__":
    main()
