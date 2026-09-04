# PostgreSQL互換ワイヤープロトコル

外部I/Fは独自プロトコルではなく、**PostgreSQLワイヤープロトコル(v3)のサブセット**
を実装する。これにより既存のPostgreSQLドライバ資産（JDBC/psycopg/node-postgres/
Npgsql/pgx等）がそのまま接続できることを狙っている。

## 採用範囲（サブセット）

| 実装する | 実装しない（初期スコープ外） |
| :--- | :--- |
| 認証ハンドシェイク（デフォルト`trust`相当、`--user`指定時のみcleartext password） | SCRAM/MD5等の認証方式 |
| Simple Query プロトコル（`Query`メッセージ） | `COPY`系プロトコル、LISTEN/NOTIFY |
| Extended Query プロトコル（`Parse`/`Bind`/`Describe`/`Execute`/`Sync`/`Close`/`Flush`） | バイナリ形式のパラメータ（Bindの値側。テキストのみ） |
| 結果値のバイナリ形式（`int8`/`float8`/`bool`/`bytea`/`timestamp`の5型のみ。下記参照） | NUMERICのバイナリ形式（base-10000桁グループの複雑な符号化。実装しない——後述） |
| `RowDescription`/`DataRow`/`CommandComplete` | 行数制限付き実行・`PortalSuspended`（`Execute`のmaxRowsは無視） |
| エラー応答（`ErrorResponse`） | 詳細なSQLSTATEコード体系（簡略化したコードで代用） |

このスコープを勝手に広げない。範囲を広げる場合は、まず仕様書を更新してから
着手する（**フェーズ④Step 5でExtended Queryを採用に切り替えた実例**:
`pgx`/pgJDBC/Npgsqlがデフォルト設定でSimple Queryのみでは接続できない
——`execdb_spec.md`§8参照——ことが判明したため、着手前に仕様書§8の表を
更新してからStep 5に着手した）。

**結果値のバイナリ形式が必要になった経緯（フェーズ④Step 5、実機確認で判明）:**
テキスト形式のみで実装したところ、`pgx`のデフォルト（Extended Query）接続で
`int8`/`float8`/`numeric`/`bool`/`bytea`/`timestamp`列が軒並み失敗した
（`invalid length for int8: 1`等のエラー、`bool`/`bytea`は**エラーにならず
黙って誤った値**を返す方が危険だった）。原因は`pgx`が`Bind`メッセージの
`resultFormatCodes`で、これらの型に対しデフォルトでバイナリ形式を要求して
くるため。`ParameterDescription`同様に「決め打ちで実装せず実機で確認する」
方針どおり、実際に`pgx`を繋いで初めて判明した。NUMERICだけは対象外とした
——SQLiteのNUMERIC親和性は内部的に必ずINTEGERかREALのいずれかで格納され
（真の任意精度十進数を持たない）、Postgresの複雑なバイナリNUMERIC形式を
実装する代わりに、`columnOID`がNUMERIC親和性の宣言型を実行時のGo動的型
（int64/float64）で`int8`/`float8`へ振り分けることで、`pgx`がそもそも
NUMERIC OIDに対してバイナリを要求する状況自体を発生させない設計にした
（詳細は`cmd/execdb/pgtype.go`の`columnOID`/`affinityOID`のdocコメント参照）。

## 型マッピング

SQLiteは動的型付け（型アフィニティ）のため、列の値の型が固定されない。
`RowDescription`で返すPostgres型OID（`int4`, `text`, `bytea`等）への変換ルールは、
**主要ドライバ（pgJDBC/psycopg等）で実接続検証しながら確定する**（現時点で
確定した対応表はない。決め打ちで実装せず、実際に繋いで確認すること）。

**フェーズ④への申し送り（フェーズ②Step 1実測済み）:** `sql.Rows.ColumnTypes()`
は`modernc.org/sqlite`で`Next()`呼び出し前でも`DatabaseTypeName()`/`ScanType()`/
`Nullable()`が正しい値を返すことを実測確認済み（`INTEGER`/`REAL`/`TEXT`/`BLOB`/
`NUMERIC`列、および式列——`dbType`は空文字列、`scanType`は`int64`——で確認。
詳細は`.claude/rules/sqlite-quirks.md`「`ColumnTypes()`は`Next()`呼び出し前でも
正しい値を返す」節）。型マッピング実装時はこのAPIをそのまま使ってよく、
「`Next()`前は不定かもしれない」と慎重になる必要はない。

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

## 実装時の気づき（フェーズ①Step 4、`psql`実接続で確認）

- **`psql`（Debianのビルド）はGSSAPI有効ビルドのため、`StartupMessage`の前に
  `GSSENCRequest`(80877104) → `SSLRequest`(80877103) の順で前置リクエストを
  送ってくる。** 両方に対して1バイト`'N'`（非対応、平文続行）を返さないと、
  クライアントが応答を待ち続けてハング状態になり、原因が非常に分かりにくい。
  ハンドシェイク処理はこの2つ（と`CancelRequest`）をループで先に処理し、
  実際の`StartupMessage`（プロトコルバージョン`196608`）が来て初めて先へ進む
  構成にすること。
- **UNIX Domain Socket経由で`psql -h <dir> -p <port>`接続する場合、libpqは
  ソケットファイル名を`<dir>/.s.PGSQL.<port>`という固定命名規則で探しに行く。**
  ExecDBの`-s`/`--socket`は任意のパスをそのまま`net.Listen("unix", path)`する
  だけなので、`psql`から接続確認したい場合は、`-s`に渡すパス自体を
  `/tmp/.s.PGSQL.5432`のような命名にしておく必要がある（任意パス名のままでは
  `psql`側が正しいファイルを見つけられずエラーになる。これはlibpq側の制約で
  あり、ExecDB側の不具合ではない）。他ドライバ（psycopg等）でも同様の規約が
  ある場合は接続確認時に確認すること。
- 上記2点は`cmd/execdb`のテスト（`pgwire.go`の実機確認、実際に`psql`で
  `SELECT`/DDL拒否/複文バイパス拒否/TCP+UDS同時待受/stale socket除去まで
  確認済み）で判明した。

## 実装時の気づき（フェーズ①Step 5、`pgx`実接続で確認）

- **`pgx`（Go）はデフォルトでExtended Queryプロトコル（`Parse`/`Bind`/
  `Execute`）を使う。** ExecDBはSimple Queryのみ実装しているため、何も
  指定しないと`conn.QueryRow`等の初回呼び出しで`unsupported message type
  'P'`エラーになる。接続文字列に`default_query_exec_mode=simple_protocol`
  を付与すると、`pgx`はSimple Queryのみを使うようになり接続できる
  （`tests/pgclient`のusageメッセージ・`tests/e2e.sh`参照）。`psql`は
  デフォルトでSimple Queryを使うため、この問題は`psql`では顕在化しない
  ——つまり「`psql`で繋がった」だけでは他ドライバの互換性を保証しない、
  という教訓でもある。フェーズ④で他ドライバ確認する際は、まずデフォルト
  設定で試し、Extended Query前提のドライバであれば同様の「Simple Query
  強制」オプションの有無を確認すること。
- **全列をOID 25(text)固定で返す実装（フェーズ①の暫定仕様）は、`pgx`の
  型チェックの厳しさによって実際に制約として顕在化する。** `psql`は
  テキスト表示するだけなので気づきにくいが、`pgx`は`RowDescription`の
  型情報を厳格に見ており、text型の列を`*int`へ`Scan`しようとすると
  `cannot scan text (OID 25) in text format into *int`のようなエラーで
  拒否される（`*string`へのScanは常に成功する）。フェーズ④で本来の型
  マッピングを実装するまでは、`pgx`等の型に厳格なドライバから使う場合、
  呼び出し側は数値列であっても文字列としてScanする必要がある。

## トランザクションの真の並行分離（フェーズ②Step 5で解決）

フェーズ①では`engine.DB`の`Exec`/`Query`/`QueryRow`が単一の`keeper`コネクション
経由に統一されていたため、複数のpgwireクライアント（またはREPL＋pgwire）が
**同時に**`BEGIN`〜`COMMIT`を実行すると、物理的には同じSQLiteコネクションを
共有しているため、あるクライアントのトランザクション中に別クライアントの文が
紛れ込む可能性がある、という制約があった（§8が要求する「コネクション単位の
トランザクション分離」を満たしていなかった）。

**フェーズ②で解消済み。** `engine.Session`（`db.Session(ctx)`）が専有コネクション
を配るようになり（Step 3）、`cmd/execdb`側もTCP/UDS**1接続につき1
`engine.Session`**を割り当てるよう結線した（`handleConnection`、Step 5）。
REPLも同様に1本の`Session`を張る（`runREPL`）。これにより`BEGIN`/`COMMIT`/
`ROLLBACK`はSession固有の`*sql.Conn`上で実行され、SQLite自身のロック機構
（`memdb` VFS、`.claude/rules/sqlite-quirks.md`参照）がクライアント間の分離を
提供する——ExecDB独自の同時実行制御は実装していない（仕様書§2の方針通り）。

`tests/pgclient`の`checkTransactionIsolation`（2接続でBEGIN/INSERT→他方から
見えない→COMMIT→見える）で自動検証済み。ただし`memdb`の性質上、これは
「真の非ブロック・スナップショット分離」ではなく「直列化による分離」である点に
注意（他セッションが書き込み中の読み取りは`busy_timeout`の範囲でブロックされ、
書き込みが確定した後の値を見る。`.claude/rules/sqlite-quirks.md`参照）。

### `ReadyForQuery`のステータスバイト（`'I'`/`'T'`/`'E'`）

`handleSimpleQuery`（`cmd/execdb/pgwire.go`）が接続ごとに`txState`を追跡し、
`BEGIN`成功で`'T'`、`COMMIT`/`ROLLBACK`/`END`成功で`'I'`、`'T'`中にSQL文が
エラーになると`'E'`に遷移する。**`'E'`中は`COMMIT`/`ROLLBACK`/`END`以外の文を
SQLSTATE `25P02`（"current transaction is aborted"）で拒否**し、実行そのものは
行わない（表示だけ`'E'`にして実行を通す中途半端な実装は、txStatusを厳密に見る
pgx/JDBC等のドライバを混乱させるため採用しなかった）。`tests/pgclient`の
`checkFailedTransactionState`で自動検証済み。

### クエリキャンセルの2系統（クライアント切断 と `CancelRequest`、フェーズ④Step 6で後者を実装）

クエリキャンセルには意図的に別々の2つの経路がある。

1. **クライアント切断時の自動キャンセル**（フェーズ②Step 5から）:
   クエリ実行中は`watchForDisconnect`（`pgwire.go`）が別goroutineで`conn`への
   1バイトRead待ちを行うことで、クライアントの切断（あるいは予期しない
   データ送出）を検知して`cancel()`する。`tests/pgclient`の
   `checkDisconnectDuringQuery`（クエリのcontextをタイムアウトさせてpgxに
   接続を諦めさせ、その後別接続がすぐに繋がることを確認）で自動検証済み。
2. **真の`CancelRequest`プロトコル**（別接続からの明示キャンセル要求。
   `BackendKeyData`のPID/secretで対象を特定する仕組み、フェーズ④Step 6で実装）:
   `pgcancel.go`の`globalCancelRegistry`（プロセス全体で1つ、`cancelKey{pid,
   secret}` → `*connCancel`のmap）が実体。`BackendKeyData`はもう`0, 0`固定では
   なく、接続ごとに`register()`で採番した実際の値を送る。`performHandshake`が
   `CancelRequest`を受け取ると対象の`connCancel.cancelCurrent()`を呼び、
   実行中のクエリがあればキャンセルする（無ければ無音のno-op、実PostgreSQL
   同様）。`tests/pgclient`の`checkCancelRequest`（別接続の
   `conn.PgConn().CancelRequest(ctx)`で長時間クエリを中断し、**中断後も同じ
   接続で次のクエリが使えること**まで確認）で自動検証済み。

**両者は`cc.begin(cancel)`/`end()`という同じ仕組みを共有する**
（`pgcancel.go`の`connCancel`）。`context.CancelFunc`は一度きりしか使えない
ため、接続の生存期間全体に対して固定の`cancel`を1つだけ使い回すと、
（`watchForDisconnect`にせよ`CancelRequest`にせよ）どちらか一方が一度でも
発火した時点で、その接続の**以後すべてのクエリ**が`context canceled`で
永久に失敗するようになる、という致命的な設計ミスをフェーズ④Step 6の実装中に
発見・修正した（`CancelRequest`は「そのクエリだけを中断し、接続自体は
使い続けられる」という実PostgreSQLの重要な性質を持つため、この設計ミスは
`CancelRequest`を実装して初めて表面化した——`watchForDisconnect`単体では
「発火後は接続を捨てる」運用だったため、同じバグが隠れたまま気づかれずに
残っていた）。修正: `handleConnection`のメインループが**クエリ（メッセージ）
ごとに新しい`context.WithCancel`を作り**、`cc.begin(cancel)`で登録・処理後に
`end()`で解除する（`interrupt.go`の`replInterrupts.begin`とまったく同じ
パターン）。ループ内では`defer end()`ではなく**明示呼び出し**にする点に注意
——`for`ループの中で`defer`すると、そのループが回っている関数
（`handleConnection`）全体が終わるまで実行されず、次のクエリが始まる前に
確実に解除されない。
