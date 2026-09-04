# ExecDB

DBエンジンとデータ領域を1つの実行ファイル内に保持する、環境構築不要の
ポータブルな単一バイナリRDBMSです。SQLite互換のSQLをそのまま使え、
セットアップは不要、データの永続化は実行ファイルとして書き出す
スナップショット方式で行います。

別途データベースファイルを用意する必要はなく、環境構築やコンテナの
ボリュームマウントも不要です。バイナリを1つ実行するだけで動きます。
すべての操作はインメモリのSQLite互換エンジン（`modernc.org/sqlite`）上で
行われ、永続化はその時点のメモリ上の状態を埋め込んだ新しい実行ファイルを
書き出すことで実現します。生成された「スナップショット」はそのまま実行
したり、他の人と共有したり、ファイルとしてコミットしたりできます。

外部クライアント（ORM、DBツール、各言語のドライバ）は、PostgreSQL互換
ワイヤープロトコルのサブセット経由で接続できるため、既存のPostgres用
ドライバ資産（JDBC、psycopg、node-postgres、Npgsql、pgx等）が、各ドライバ
自身のデフォルト接続設定のままそのまま利用できます。

## インストール

```sh
go install github.com/amisonnet8/execdb/cmd/execdb@latest
```

Go 1.26以降が必要です。Linux/macOS/Windows（amd64/arm64）向けのビルド済み
バイナリは、バージョンタグをpushするたびに
[GitHub Releases](https://github.com/amisonnet8/execdb/releases)へ自動公開
されます。ソースからビルドしたくない場合はそちらから該当プラットフォーム用の
ファイルを取得してください（この文書作成時点では、まだタグをpushしておらず
リリースはありません）。

## クイックスタート

```sh
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb> CREATE TABLE t(a INTEGER);
execdb> INSERT INTO t VALUES (1);
execdb> .snapshot mydb
Wrote mydb
execdb> .exit
```

`mydb`は、そのテーブルと行を埋め込んだ、単独で動作する実行ファイルに
なっています。

```sh
chmod +x mydb   # Windowsでは不要
./mydb
```

```
ExecDB v...
Loaded snapshot: mydb
Enter ".help" for usage hints.
execdb> SELECT * FROM t;
1
```

`-p :5432`を付けて起動すると、そのデータをPostgreSQL互換ワイヤー
プロトコル経由でも公開できます。既存のPostgresクライアント・ドライバから
そのまま接続できます。

## 詳しくは

- **[`docs/usage/`](docs/usage/)** — CLIオプション・REPLコマンドのリファレンス
- **[`docs/examples/`](docs/examples/)** — CIテスト用DB、バグ再現データの共有、
  モックAPIサーバー、SQLサンドボックス
- **[`docs/spec/execdb_spec.md`](docs/spec/execdb_spec.md)** — 詳細な設計・仕様書
- **[`PLAN.md`](PLAN.md)** — 実装の進捗

English version: [README.md](README.md)

## License

MIT — [LICENSE](LICENSE) を参照。
