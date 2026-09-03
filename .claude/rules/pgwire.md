# PostgreSQL互換ワイヤープロトコル

外部I/Fは独自プロトコルではなく、**PostgreSQLワイヤープロトコル(v3)のサブセット**
を実装する。これにより既存のPostgreSQLドライバ資産（JDBC/psycopg/node-postgres/
Npgsql/pgx等）がそのまま接続できることを狙っている。

## 採用範囲（サブセット）

| 実装する | 実装しない（初期スコープ外） |
| :--- | :--- |
| 認証ハンドシェイク（デフォルト`trust`相当、`--user`指定時のみcleartext password） | SCRAM/MD5等の認証方式 |
| Simple Query プロトコル（`Query`メッセージ） | Extended Query プロトコル（`Parse`/`Bind`/`Execute`） |
| `RowDescription`/`DataRow`/`CommandComplete` | `COPY`系プロトコル、LISTEN/NOTIFY |
| エラー応答（`ErrorResponse`） | 詳細なSQLSTATEコード体系（簡略化したコードで代用） |

このスコープを勝手に広げない（例えば Extended Query プロトコルへの対応を
先回りして実装しない）。範囲を広げる場合は、まず仕様書を更新してから着手する。

## 型マッピング

SQLiteは動的型付け（型アフィニティ）のため、列の値の型が固定されない。
`RowDescription`で返すPostgres型OID（`int4`, `text`, `bytea`等）への変換ルールは、
**主要ドライバ（pgJDBC/psycopg等）で実接続検証しながら確定する**（現時点で
確定した対応表はない。決め打ちで実装せず、実際に繋いで確認すること）。

## 認証（オプトイン、Zero-Authがデフォルト）

- **デフォルト（`--user`未指定）:** 常にZero-Auth（`trust`相当）。
- **`--user NAME`指定時のパスワード取得（優先順位）:**
  1. 環境変数 `EXECDB_PASSWORD` が設定されていればそれを使用（対話プロンプトなし）。
  2. 未設定かつREPLモード: 標準入力から対話的に入力させる。
  3. 未設定かつ`--no-repl`: エラーで起動中止（対話する相手がいないため）。
- 「ユーザー」という概念（マルチユーザー管理、権限分離）は持たない。単一の
  名前＋パスワードの組を、接続に必要な認証キーとして扱うのみ。

## トランスポート

TCPとUNIX Domain Socketの両方に対応する。同一のプロトコル実装をトランスポート
層だけ差し替えて共有する（プロトコル実装をトランスポートに依存させない）。
