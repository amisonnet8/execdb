# 環境構築ゼロのモックAPI/データベースサーバー

*English version: [mock-server.md](mock-server.md)*

本物のPostgreSQLサーバーをインストール・設定することなく、任意の
PostgreSQLクライアントやORMをExecDBへ向けられる。フェイクなバックエンド
に対するフロントエンド開発、デモ、使い捨ての結合テストに便利。

## 起動する

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

これはREPL*と*PostgreSQLワイヤープロトコルのリスナーを同時に起動する
——どちらから挿入したデータも、もう一方から見える(`-p`/`-s`/`-n`について
詳しくは[CLIオプション](../usage/cli-options_ja.md)を参照)。外部
インターフェースにパスワードを要求したい場合は`-u NAME`を追加する
([認証](../usage/cli-options_ja.md#認証)参照)。

## `psql`から接続する

```sh
psql -h 127.0.0.1 -p 5432 -U any -d any -c 'SELECT * FROM products;'
```

## アプリケーションから接続する

以下のほぼすべての接続は、各ドライバ自身の**デフォルト設定**をそのまま
使っている——ExecDB固有のフラグや回避策は不要(仕様書§8が掲げる互換性の
ゴール)。**唯一の例外はNpgsql(.NET)**——下記のその項を参照。この
インターフェース経由で拒否されるのは`DDL`(`CREATE TABLE`等)だけ——DMLと
トランザクションは通常通り動く。アクセス制御モデルの詳細は
[`docs/spec/execdb_spec_ja.md`](../spec/execdb_spec_ja.md)§2を参照。

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

Npgsqlだけは接続文字列に`Server Compatibility Mode=NoTypeLoading`が必要
——これが無いと、Npgsql自身の接続確立処理が(型キャッシュを構築するために)
ExecDBに存在しない本物のPostgresシステムカタログへクエリを送ってしまう。
これはNpgsql非対応DB(CockroachDB/Redshiftのユーザー向けに案内されている
のと同じ)接続時の標準オプションであり、ExecDB側で追加したものではない。

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

### ODBC (psqlODBC) — Excel、Power BI、Access、その他のODBCツール

公式のPostgreSQL ODBCドライバ(Debian/Ubuntuでは`odbc-postgresql`、
Windowsの「ODBCデータソースアドミニストレーター」では"PostgreSQL
Unicode")をインストールし、次のように接続する:

```
Driver=PostgreSQL Unicode;Server=127.0.0.1;Port=5432;Database=any;Uid=any;Pwd=;
```

```sh
isql -v -k "Driver=PostgreSQL Unicode;Server=127.0.0.1;Port=5432;Database=any;Uid=any;Pwd=;"
```

単純なクエリだけでなく、ExecDBはこうしたツールがテーブル・列の一覧を
見せるために使うスキーマブラウズ呼び出し(`SQLTables`/`SQLColumns`)にも
応答する——ExcelやPower BIの「Get Data > From ODBC」は、空のリストでは
なく実際のExecDBのスキーマを見ることになる。

## この実例のもと

リポジトリ内の`tests/drivers/`・`tests/pgclient/`は、`make test`の一部
として、DDL拒否・トランザクション・キャンセルのチェックも含め、まさに
このような接続をまとめて実行している——これらの言語について、より完全で
検証済みのリファレンスコードが欲しい場合はそちらを参照するとよい。
