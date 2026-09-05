// Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
// follow-up): Rust's postgres/tokio-postgres crate, against a running
// ExecDB instance already seeded with table t(a INTEGER) (see
// tests/e2e.sh).
//
// Unlike the other 5 verified drivers, this check uses prepare_typed
// (self-declaring each parameter's OID up front, the same thing pgJDBC's
// setInt/Npgsql already do implicitly) rather than the crate's plain
// prepare/query. That is not just a style choice -- it sidesteps a real
// interoperability problem this driver exposed: tokio-postgres's default
// (type-unaware) query path leaves a parameter's OID unspecified and asks
// the server to resolve it via ParameterDescription, and when told 0
// (ExecDB's "unspecified" answer, tolerated by every other driver), it
// queries pg_catalog.pg_type/pg_range/pg_namespace to look up what type 0
// supposedly is -- which does not exist, so it asks again, forever, until
// its stack overflows. Declaring the type up front avoids the lookup
// entirely. See .claude/rules/pgwire.md for the full story, including a
// second, independent bug this driver's stricter type-checking exposed
// and that ExecDB itself needed fixing (a Describe/Execute OID
// inconsistency affecting any client that only Describes a statement,
// never a portal -- a valid, allowed message sequence this crate happens
// to use by default).
use postgres::types::Type;
use postgres::{Client, NoTls};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: check <connection-string>");
        std::process::exit(2);
    }

    let mut client = Client::connect(&args[1], NoTls).expect("connect");
    check(&mut client);
    println!("OK");
}

fn check(client: &mut Client) {
    let stmt = client.prepare_typed("SELECT $1", &[Type::INT4]).unwrap();
    let row = client.query_one(&stmt, &[&1i32]).unwrap();
    let one: i64 = row.get(0);
    assert_eq!(one, 1, "SELECT $1 (1) returned {}", one);

    let stmt = client.prepare_typed("SELECT $1", &[Type::FLOAT8]).unwrap();
    let row = client.query_one(&stmt, &[&3.5f64]).unwrap();
    let f: f64 = row.get(0);
    assert_eq!(f, 3.5, "SELECT $1 (3.5) returned {}", f);

    let stmt = client.prepare_typed("SELECT $1", &[Type::TEXT]).unwrap();
    let row = client.query_one(&stmt, &[&"hello"]).unwrap();
    let s: String = row.get(0);
    assert_eq!(s, "hello", "SELECT $1 ('hello') returned {}", s);

    let row = client.query_one("SELECT x'00ff'", &[]).unwrap();
    let blob: Vec<u8> = row.get(0);
    assert_eq!(blob, vec![0x00, 0xff], "SELECT x'00ff' returned {:?}", blob);

    let row = client.query_one("SELECT NULL", &[]).unwrap();
    let null: Option<String> = row.get(0);
    assert_eq!(null, None, "SELECT NULL returned {:?}", null);

    // spec §2: DDL must be rejected via the external I/F, and the crate
    // must surface it as a DbError carrying SQLSTATE 42501 (spec §8).
    match client.execute("CREATE TABLE rust_should_not_exist(a INTEGER)", &[]) {
        Ok(_) => panic!("expected CREATE TABLE to be rejected via the external I/F"),
        Err(e) => {
            let code = e.code().map(|c| c.code()).unwrap_or_default();
            assert_eq!(code, "42501", "expected SQLSTATE 42501, got {} ({})", code, e);
        }
    }

    // A basic write/read round trip against the table tests/e2e.sh seeded.
    let stmt = client
        .prepare_typed("INSERT INTO t VALUES ($1)", &[Type::INT4])
        .unwrap();
    client.execute(&stmt, &[&777008i32]).unwrap();
    let stmt = client
        .prepare_typed("SELECT count(*) FROM t WHERE a = $1", &[Type::INT4])
        .unwrap();
    let row = client.query_one(&stmt, &[&777008i32]).unwrap();
    let n: i64 = row.get(0);
    assert_eq!(n, 1, "expected count=1 after INSERT, got {}", n);
}
