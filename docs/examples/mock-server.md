# A zero-config mock API / database server

Point any PostgreSQL client or ORM at ExecDB without installing or
configuring an actual PostgreSQL server. Useful for frontend development
against a fake backend, demos, or throwaway integration testing.

## Start it

```sh
execdb <<'SQL'
CREATE TABLE products(id INTEGER PRIMARY KEY, name TEXT, price REAL);
INSERT INTO products(name, price) VALUES ('Widget', 9.99), ('Gadget', 19.99);
.snapshot demo
.exit
SQL

chmod +x demo
./demo -p 127.0.0.1:5432
```

This starts the REPL *and* the PostgreSQL wire protocol listener at the
same time — data you insert from either side is visible to the other (see
[CLI options](../usage/cli-options.md) for `-p`/`-s`/`-n`). Add `-u NAME` if
you want the external interface to require a password
([Authentication](../usage/cli-options.md#authentication)).

## Connect from `psql`

```sh
psql -h 127.0.0.1 -p 5432 -U any -d any -c 'SELECT * FROM products;'
```

## Connect from your application

Every connection below uses each driver's own **default settings** — no
ExecDB-specific flags or workarounds needed (spec §8's compatibility goal).
Only `DDL` (`CREATE TABLE` etc.) is rejected over this interface — DML and
transactions work normally; see
[`docs/spec/execdb_spec.md`](../spec/execdb_spec.md) §2 for the access-control
model.

### Python (psycopg2)

```python
import psycopg2

conn = psycopg2.connect("host=127.0.0.1 port=5432 user=any dbname=any")
cur = conn.cursor()
cur.execute("SELECT name, price FROM products")
for name, price in cur.fetchall():
    print(name, price)
```

### Node.js (node-postgres)

```js
const { Client } = require('pg');

const client = new Client({ connectionString: 'postgres://any@127.0.0.1:5432/any' });
await client.connect();
const res = await client.query('SELECT name, price FROM products');
console.log(res.rows);
```

### Go (pgx)

```go
conn, err := pgx.Connect(ctx, "postgres://any@127.0.0.1:5432/any")
rows, err := conn.Query(ctx, "SELECT name, price FROM products")
```

### Java (JDBC)

```java
Connection conn = DriverManager.getConnection("jdbc:postgresql://127.0.0.1:5432/any?user=any");
Statement st = conn.createStatement();
ResultSet rs = st.executeQuery("SELECT name, price FROM products");
```

## Where these examples come from

`tests/drivers/` and `tests/pgclient/` in the repository run exactly these
kinds of connections (plus DDL-rejection, transaction, and cancellation
checks) as part of `make test` — if you want more complete, verified
reference code for any of these languages, that's the place to look.
