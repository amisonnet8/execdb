#!/usr/bin/env python3
"""Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
Step 7): psycopg2, used with its own default connection settings (no
ExecDB-specific workaround flags), against a running ExecDB instance
already seeded with table t(a INTEGER) (see tests/e2e.sh).

psycopg2 wraps every statement in an implicit BEGIN/COMMIT (autocommit is
off by default) by sending literal "BEGIN"/"COMMIT" SQL over the wire, so
this check never issues those itself -- it only calls conn.commit()/
conn.rollback() and lets psycopg2 generate the SQL text, exercising the
same plain-SQL-forwarding behavior tests/pgclient already covers from Go.
"""
import sys

import psycopg2


def main():
    if len(sys.argv) != 2:
        print("usage: check.py <dsn>", file=sys.stderr)
        sys.exit(2)

    conn = psycopg2.connect(sys.argv[1])
    try:
        check(conn)
    finally:
        conn.close()
    print("OK")


def check(conn):
    cur = conn.cursor()

    cur.execute("SELECT 1")
    (one,) = cur.fetchone()
    assert one == 1, f"SELECT 1 returned {one!r}"

    cur.execute("SELECT 3.5")
    (f,) = cur.fetchone()
    assert f == 3.5, f"SELECT 3.5 returned {f!r}"

    cur.execute("SELECT 'hello'")
    (s,) = cur.fetchone()
    assert s == "hello", f"SELECT 'hello' returned {s!r}"

    cur.execute("SELECT x'00ff'")
    (blob,) = cur.fetchone()
    assert bytes(blob) == b"\x00\xff", f"SELECT x'00ff' returned {blob!r}"

    cur.execute("SELECT NULL")
    (null,) = cur.fetchone()
    assert null is None, f"SELECT NULL returned {null!r}"
    conn.commit()

    # spec §2: DDL must be rejected via the external I/F, and psycopg2 must
    # surface it as a structured error with SQLSTATE 42501 (spec §8), not
    # just some generic failure.
    try:
        cur.execute("CREATE TABLE psycopg_should_not_exist(a INTEGER)")
        conn.commit()
        raise AssertionError("expected CREATE TABLE to be rejected via the external I/F")
    except psycopg2.Error as e:
        conn.rollback()
        assert e.pgcode == "42501", f"expected SQLSTATE 42501, got {e.pgcode!r} ({e})"

    # A basic write/read round trip against the table tests/e2e.sh seeded.
    cur.execute("INSERT INTO t VALUES (777001)")
    conn.commit()
    cur.execute("SELECT count(*) FROM t WHERE a = 777001")
    (n,) = cur.fetchone()
    assert n == 1, f"expected count=1 after INSERT+COMMIT, got {n}"


if __name__ == "__main__":
    main()
