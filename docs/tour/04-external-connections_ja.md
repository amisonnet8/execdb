# 4. 他のツールから話しかける

*English version: [04-external-connections.md](04-external-connections.md)*

ここまではすべてREPLを直接使ってきた。起動時に`-p ADDR`を追加すると、
ExecDBはそのアドレス上でPostgreSQL互換のワイヤープロトコルも話すように
なる——REPLとネットワークリスナーは、同じ生きたデータを同時に共有する:

```sh
./mydb -p 127.0.0.1:5432
```

```
ExecDB v...
Loaded snapshot: mydb
Listening on 127.0.0.1:5432 (PostgreSQL wire protocol)
Enter ".help" for usage hints.
execdb>
```

これで任意のPostgresクライアントが接続できる——`psql`でも、本物の
言語ドライバでも:

```sh
psql -h 127.0.0.1 -p 5432 -U any -d any -c 'SELECT * FROM todos;'
```

```
 id |      task      | done
----+----------------+------
  1 | write the tour |    0
  2 | ship it        |    0
(2 rows)
```

ユーザー/パスワード/データベースのセットアップは一切不要だった——上の
`any`/`any`は文字通りただの埋め草で、そのまま受け入れられている。これは
[モックAPIサーバーの実例](../examples/mock-server_ja.md)と同じ仕掛けで、
そちらにはPython、Node.js、Go、Java、.NET、PHP、Ruby、Rust、ODBCツール
(Excel、Power BI、Access)向けのコピペ可能な接続例も載っている——
いずれもそのドライバ自身のデフォルト設定で動く(Npgsqlだけは接続文字列に
1つオプションが余分に必要——理由はそのページを参照)。

## 読み書きはできるが、スキーマ変更はできない

外部インターフェースはクエリの実行と行の変更はできるが、`CREATE`/
`ALTER`/`DROP`はできない——DDLはREPLへのローカルアクセスを持つ人専用に
予約されている:

```sh
psql -h 127.0.0.1 -p 5432 -U any -d any -c 'CREATE TABLE hack(x INTEGER);'
```

```
ERROR:  DDL statements are not allowed via external interface
```

これは回避すべきバグではなく、このインターフェースが基づいている
アクセス制御モデルそのものだ——完全な理由は
[`docs/spec/execdb_spec_ja.md`](../spec/execdb_spec_ja.md)§2を参照。

## パスワードを要求する

デフォルトでは、そのポートに到達できる人なら誰でも接続できる
(Zero-Auth)。`-u NAME`を追加すると、パスワードも要求するようになる:

```sh
EXECDB_PASSWORD=secret ./mydb -p 127.0.0.1:5432 -u alice
```

```sh
PGPASSWORD=secret psql -h 127.0.0.1 -p 5432 -U alice -d any -c 'SELECT 1;'
```

間違ったパスワードはそのまま拒否される:

```
psql: error: connection to server ... failed: ERROR:  password authentication failed for user "alice"
```

これは外部インターフェースだけを保護する——REPL自体はすでに同じマシンへ
のアクセスを必要とするため、決して認証されない。パスワード決定の完全な
優先順位は[認証](../usage/cli-options_ja.md#認証)を参照。

## 無人で動かす

`-n`/`--no-repl`はREPLを完全に排除し、外部インターフェースだけを提供
する——バックグラウンドサービスとしてExecDBを動かすためのもの。この
モードでは`.snapshot`コマンドが使えないため、「決して自動保存しない」
という原則に1つだけ例外を設けている: シャットダウン時に保存する。

```sh
./mydb -p :5432 -n &
kill %1
```

```
Saved snapshot to mydb
```

**次に読むもの:** 全CLIフラグ・REPLコマンドのリファレンスは
[`docs/usage/`](../usage/)、完結したタスク指向のウォークスルー(CIテスト
用DB、バグ再現の共有、SQLサンドボックス)は[`docs/examples/`](../examples/)、
このツアーで扱った内容すべての設計上の理由は
[`docs/spec/execdb_spec_ja.md`](../spec/execdb_spec_ja.md)を参照。
