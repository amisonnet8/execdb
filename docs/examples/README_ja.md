# 実例集

*English version: [README.md](README.md)*

ExecDBのよくある使い方を、タスク指向で説明したウォークスルー集。ExecDBが
初めてで、順を追ったガイドから始めたいなら、代わりに
[`docs/tour/`](../tour/README_ja.md)を見てほしい——ここの各ページは
自己完結していて、基本を知っている前提で書かれている。コマンド/フラグの
プレーンなリファレンスは[`docs/usage/`](../usage/README_ja.md)、設計の
理由を詳しく知りたい場合は
[`docs/spec/execdb_spec_ja.md`](../spec/execdb_spec_ja.md)を参照。

- **[CI/CD: 瞬時に立ち上がるテスト用DB](ci-testing_ja.md)** — スキーマ+
  シードデータをバイナリへ一度焼き込んでおけば、あとは各テストジョブが
  それを実行するだけでよい。
- **[バグ再現データを実行可能なファイルとして共有する](snapshot-sharing_ja.md)**
  — 壊れたデータ状態を`.snapshot`し、手順書の代わりにファイルを渡す。
- **[環境構築ゼロのモックAPI/DBサーバー](mock-server_ja.md)** — psql、
  psycopg、node-postgres、pgx、JDBC、Npgsql、PDO_PGSQL、`pg` gem、Rustの
  `postgres`クレート、任意のODBCツール(Excel、Power BI、Access)を、
  インストール不要でExecDBへ接続する(各言語ごとのコピペ可能な接続コード
  付き)。
- **[環境構築ゼロのSQLサンドボックス](sql-sandbox_ja.md)** — 学習・実験用の
  フル機能SQLエンジン(view/index/trigger/transaction)。インストールも
  片付けも不要。

より網羅的で、機械的に検証されたリファレンスコードが欲しい場合は、
このリポジトリ内の`tests/pgclient/`(`make test`が実行する、Go/pgxによる
検証)がここでの接続例の、より詳しい版に当たる。他言語(Python、Node.js、
Java、.NET、PHP、Ruby、Rust、ODBC)ドライバの検証は別リポジトリ
[`execdb-drivers`](https://github.com/amisonnet8/execdb-drivers)にある。
