# A zero-config mock API / database server

*日本語版はこちら: [mock-server_ja.md](mock-server_ja.md)*

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

Nearly every connection below uses each driver's own **default settings**
— no ExecDB-specific flags or workarounds needed (spec §8's compatibility
goal). **Npgsql (.NET) is the one exception** — see its section below.
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

### .NET (Npgsql)

```csharp
using Npgsql;

await using var conn = new NpgsqlConnection(
    "Host=127.0.0.1;Port=5432;Username=any;Database=any;Server Compatibility Mode=NoTypeLoading");
await conn.OpenAsync();

await using var cmd = new NpgsqlCommand("SELECT name, price FROM products", conn);
await using var reader = await cmd.ExecuteReaderAsync();
while (await reader.ReadAsync())
    Console.WriteLine($"{reader.GetString(0)} {reader.GetDouble(1)}");
```

Npgsql needs `Server Compatibility Mode=NoTypeLoading` in the connection
string — without it, Npgsql's own connection setup queries real
PostgreSQL system catalogs (to build its type cache) that don't exist in
ExecDB. This is a standard Npgsql option for non-genuine-Postgres
backends (CockroachDB/Redshift users pass the same flag), not something
ExecDB adds.

### PHP (PDO_PGSQL)

```php
<?php
$pdo = new PDO("pgsql:host=127.0.0.1;port=5432;dbname=any;user=any");
foreach ($pdo->query("SELECT name, price FROM products") as $row) {
    echo "{$row['name']} {$row['price']}\n";
}
```

### Ruby (`pg` gem)

```ruby
require 'pg'

conn = PG.connect(host: '127.0.0.1', port: 5432, dbname: 'any', user: 'any')
conn.exec("SELECT name, price FROM products") do |result|
  result.each { |row| puts "#{row['name']} #{row['price']}" }
end
```

### Rust (`postgres` crate)

```rust
use postgres::{Client, NoTls};

let mut client = Client::connect("host=127.0.0.1 port=5432 dbname=any user=any", NoTls)?;
for row in client.query("SELECT name, price FROM products", &[])? {
    let name: &str = row.get(0);
    let price: f64 = row.get(1);
    println!("{name} {price}");
}
```

### ODBC (psqlODBC) — Excel, Power BI, Access, and other ODBC tools

Install the official PostgreSQL ODBC driver (`odbc-postgresql` on
Debian/Ubuntu, "PostgreSQL Unicode" in Windows' ODBC Data Source
Administrator) and connect with:

```
Driver=PostgreSQL Unicode;Server=127.0.0.1;Port=5432;Database=any;Uid=any;Pwd=;
```

```sh
isql -v -k "Driver=PostgreSQL Unicode;Server=127.0.0.1;Port=5432;Database=any;Uid=any;Pwd=;"
```

Beyond plain queries, ExecDB also answers the schema-browsing calls
(`SQLTables`/`SQLColumns`) these tools use to show you a list of tables
and columns to pick from — so "Get Data > From ODBC" in Excel or Power BI
sees your real ExecDB schema, not an empty list.

## Where these examples come from

`tests/pgclient/` in this repository runs exactly this kind of Go/pgx
connection (plus DDL-rejection, transaction, and cancellation checks) as
part of `make test`, if you want more complete, verified reference code.
The other languages' connections above are exercised the same way in the
separate [`execdb-drivers`](https://github.com/amisonnet8/execdb-drivers)
repository.
