# PostgreSQL互換ワイヤープロトコル

外部I/Fは独自プロトコルではなく、**PostgreSQLワイヤープロトコル(v3)のサブセット**
を実装する。これにより既存のPostgreSQLドライバ資産（JDBC/psycopg/node-postgres/
Npgsql/pgx等）がそのまま接続できることを狙っている。

## 採用範囲（サブセット）

| 実装する | 実装しない（初期スコープ外） |
| :--- | :--- |
| 認証ハンドシェイク（デフォルト`trust`相当、`--user`指定時のみcleartext password） | SCRAM/MD5等の認証方式 |
| Simple Query プロトコル（`Query`メッセージ） | `COPY`系プロトコル、LISTEN/NOTIFY |
| Extended Query プロトコル（`Parse`/`Bind`/`Describe`/`Execute`/`Sync`/`Close`/`Flush`） | NUMERICのバイナリ形式（base-10000桁グループの複雑な符号化。実装しない——後述） |
| 結果値・パラメータ値双方のバイナリ形式（`int2`/`int4`/`int8`/`float4`/`float8`/`bool`/`bytea`/`timestamp`。下記参照） | ─ |
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

**パラメータ側のバイナリ形式が必要になった経緯（フェーズ④Step 7、実機確認で判明）:**
Step 5では「パラメータは常にテキスト」という前提で、`Bind`がバイナリ形式の
パラメータを送ってきたら一律`ErrorResponse`で拒否する実装にしていた
（`pgx`/`psycopg`はデフォルトでパラメータをテキスト送信するため、この時点の
実機確認では問題が出なかった）。ところがStep 7で他言語ドライバ（pgJDBC）を
実際に繋いで検証したところ、`PreparedStatement.setInt`/`setDouble`等を
デフォルト設定のまま使うだけで、`Bind`のパラメータがバイナリ形式で送られてくる
ことが判明した（`binary-format parameters are not supported`で全滅）。

**原因の特定に手間取った点:** `ParameterDescription`は常にOID 0
（unspecified）を返す実装のままだったため、「サーバー側がどう答えているか」を
見ている限り、クライアントがなぜバイナリを選ぶのか説明がつかなかった。
`handleParse`（`pgextended.go`）が`Parse`メッセージ自身のパラメータ型OID配列
（`paramOIDs`）を読み捨てていたのが盲点で、一時的なデバッグ出力で`Parse`の
生の中身を見て初めて、**pgJDBCが`Parse`の時点で自ら`OID 23`（int4）を
申告しており、`ParameterDescription`の応答内容とは無関係にその自己申告した
型に基づいてバインド形式（テキスト/バイナリ）を決めている**ことが分かった
（real PostgreSQLでも同様——クライアントが型を分かっているなら`Parse`で
教えてよい、というプロトコル本来の使い方）。

**対処:** `Parse`が申告した`paramOIDs`を`preparedStatement`に保持し、
`Bind`でバイナリ形式のパラメータが来たら、そのOIDを手がかりに
`decodeBinaryParam`（`pgtype.go`）でデコードする設計に変更した。対応OIDは
結果値側（`binaryCapableOIDs`）より広く、`int2`/`int4`/`float4`を含む8種——
`columnOID`は列の型としてこれらを一度も返さないが（SQLiteのINTEGER
アフィニティは常に`int8`へ、REALは常に`float8`へ寄せる設計）、**クライアントが
自ら申告する“パラメータ”の型としては、この決め打ちとは無関係に現実に出現する**
ため、結果値側とパラメータ側でOIDの取り扱い range を独立に考える必要がある、
というのがこの発見の一般化できる教訓。クライアントが型OIDを申告せず
（`Parse`のOIDが0のまま、または省略）にバイナリで送ってきた場合は、
デコードのしようがないため従来通り拒否する（`ParameterDescription`が0を
返す設計をやめたわけではない——「サーバーが何を答えたか」と「クライアントが
何を自己申告したか」は別物、という整理が今回の核心）。

## Npgsqlは接続文字列に`Server Compatibility Mode=NoTypeLoading`が必要（フェーズ④Step 7、当初は見送っていたドライバ）

フェーズ④の当初計画では.NET（Npgsql）の実機検証を見送っていた（「常に
Extended Queryを使うため接続不可」という理由）。Step 5でExtended Queryを
実装した後、実際にNpgsqlのデフォルト設定で接続してみると、Extended Query
自体は問題なく通ったが、別の全く独立した理由で接続確立そのものが失敗した。

**原因: Npgsqlは接続確立時に独自の「型カタログのブートストラップ」を行う。**
`SELECT version();`に続けて、`pg_type`/`pg_namespace`/`pg_class`/`pg_proc`/
`pg_range`/`pg_attribute`/`pg_enum`という実在のPostgresシステムカタログを
対象にした複数のSELECTを1つのSimple Queryメッセージとしてバッチ送信して
くる（カスタム型・enum・複合型・配列型をクライアント側で解決するための
仕組み）。SQLiteにはこれらの関数・テーブルが一切無いため
`SQL logic error: no such function: version`のようなエラーになり、
アプリケーションのクエリを1つも送らないうちに接続が落ちる。pgx/psycopg/
node-postgres/pgJDBCはいずれもこの種のブートストラップを行わないため
（実機確認済み）、Npgsql固有の問題である。

**対処: 接続文字列に`Server Compatibility Mode=NoTypeLoading`を指定する。**
これはExecDB向けの独自パッチではなく、CockroachDB・Redshift等「ワイヤー
互換だが本物のPostgresではない」バックエンドに接続する際にNpgsql自身が
公式に案内している標準の接続オプションで、指定すると型カタログの
ブートストラップ自体をスキップし、組み込みの既知型（int4/text/bool等）
だけで動作するようになる。`tests/drivers/run-all.sh`が呼び出し時にこの
パラメータを付与しており、`tests/drivers/dotnet/Program.cs`自体は
ハードコードしていない（呼び出し元の責務として分離）。**この1点だけは
他の4ドライバ（pgx/pgJDBC/psycopg/node-postgres）と異なり「デフォルト
接続設定のまま」では接続できない**——execdb_spec.md§8・
`tests/drivers/README.md`にその旨を明記している。

**副次的に見つかった別の問題（Describeのパラメータ仮バインド）:** 上記を
解決して接続はできても、`SELECT $1`のような「プレースホルダそのものが
結果列になる」問い合わせで、Npgsqlの`ExecuteScalarAsync()`が
`InvalidCastException`を投げるケースが見つかった。原因は
`describeRowShape`（`cmd/execdb/pgextended.go`）が、statement-level・
portal-levelのどちらのDescribeでも常に全プレースホルダをNULLで仮バインド
して試験実行していたため——portal-level Describe（Bind後）の時点では
実際のBind値（`portal.args`）が既にあるにもかかわらずそれを使っておらず、
`SELECT $1`のNULL仮バインド結果は`columnOID`のScanTypeフォールバックで
OID 25(text)になる。pgJDBC（`getLong()`）やnode-postgres（暗黙の文字列
比較）は値取得側が文字列を許容するため表面化しなかったが、Npgsqlの
`ExecuteScalarAsync()`は宣言されたOIDどおりの厳密な型でしか値を返さない
ため、text列を`(long)`にキャストしようとして失敗した。**対処:
portal-level Describeでは`portal.args`（実際のBind値）を仮バインドに使う
よう変更**——real PostgreSQL自身も、クライアントが`Parse`で申告した
パラメータ型からこの種の列の型を決定するため、この修正はreal PostgreSQLの
挙動により近づける形になった。

**もう一つ見つかった、テストインフラ側の潜在バグ（`tests/drivers/run-all.sh`）:**
上記2つの原因究明中、Npgsqlが投げる例外のスタックトレース（stderr出力）が
一切見えず、原因調査が難航する場面があった。原因は`run-all.sh`の
`exec 3>&- 3<&- 2>/dev/null || true`という行——サーバー起動確認用に開いた
fd 3を閉じる際の警告を消すつもりだったが、**波括弧で囲まずに裸の`exec`へ
リダイレクトを付けると、そのリダイレクトは現在のシェルに対して恒久的に
適用される**（サブシェルではなく現在のシェル自身の設定を変更する`exec`の
性質上）。結果として、この行以降のスクリプト全体のstderrが`/dev/null`へ
永久にリダイレクトされてしまい、各ドライバチェックの`FAIL - ...`という
エラーメッセージも、ドライバ自身がstderrに吐くクラッシュ内容も、以後
一切表示されなくなっていた（python/node/javaは元々失敗したことがなかった
ため、この潜在バグはNpgsql検証で初めて表面化した）。**対処:
`{ exec 3>&- 3<&-; } 2>/dev/null || true`とブレースでグループ化し、
リダイレクトの適用範囲をfdクローズ自体に限定した。**

## ODBC（psqlODBC）対応: pg_catalog互換ビューの導入（フェーズ④完了後の追加）

Npgsql対応の後、ODBC（`unixodbc`+PostgreSQL公式のODBCドライバ`psqlODBC`）も
検証してほしいという要望を受け、実機で調査した。

**初手の結果: 接続すらできない。** `isql`で素朴に繋ぐと
`SQL logic error: no such table: pg_type`で即座に失敗した。デバッグ出力
（`handleSimpleQuery`/`handleParse`に一時的な`fmt.Fprintln(os.Stderr, ...)`
を仕込み、実際に送られてくるSQL文をそのまま観測する——pgJDBC/Npgsql調査と
同じ手法）で調べたところ、psqlODBCは接続直後に
`select oid, typbasetype from pg_type where typname = 'lo'`
（ラージオブジェクト型の有無チェック）を送っていた。

**2段階に切り分けて調査した。**

1. **Tier 1（軽量）**: 上記の`pg_type`単発クエリだけなら、空の`pg_type`
   テーブルを1つ用意するだけで、接続・SELECT・パラメータ化クエリ・
   INSERT・DDL拒否（42501）まですべて動くことを確認した。
2. **Tier 2（重い）**: ただし`cursor.tables()`/`cursor.columns()`
   （`SQLTables`/`SQLColumns`——Excel・Power BI・Access等が「テーブル
   一覧を見せる」ために内部で呼ぶODBC標準API）は、実際には
   `pg_class`/`pg_namespace`/`pg_attribute`/`pg_attrdef`という実在の
   Postgresシステムカタログへの本格的なJOINクエリを送ってきており、
   さらに`pg_get_expr()`/`current_schema()`というPostgres組み込み関数の
   呼び出しも含んでいた。

**ユーザーとの相談の結果、Tier 1・Tier 2の両方を実装する方針で確定した。**
以下、実装中に判明した設計上の制約と回避策。

### 設計: 実テーブルを汚さない「pg_catalog互換ビュー」（`cmd/execdb/pgcatalog.go`）

`pg_class`/`pg_namespace`/`pg_attribute`は、ExecDBの実スキーマ
（`sqlite_master`・`pragma_table_info()`）から動的に導出するSQLビューとして
定義し、**静的なデータとして書き出さない**設計にした。これにより
スキーマ変更後も常に最新の状態を反映し、メンテナンスコストがゼロになる。
`pragma_table_info(m.name)`をテーブル値関数として`sqlite_master`と相関
JOINできる（`SELECT m.name, p.* FROM sqlite_master m, pragma_table_info(m.name) p`）
ことは実測確認済み。

**当初案（ATTACH DATABASEで独立スキーマにする）は却下した。**
`ATTACH DATABASE ':memory:' AS pg_catalog`した上でその中に
`CREATE VIEW pg_catalog.pg_class AS ... FROM main.sqlite_master ...`を
定義しようとしたところ、**`view pg_class cannot reference objects in
database main`**というエラーで拒否された。SQLiteは「別データベースの
オブジェクトを参照するVIEWを、そのデータベース以外の場所に定義できない」
という制約を持つ（実測で確認、ドキュメントだけでは気づきにくい）。

**対処: 通常のATTACHではなく、`main`を自由に参照できる`TEMP`ビュー/
テーブルとして定義する。** TEMPスキーマは`main`を無制限に参照でき
（実測確認済み）、かつ`.tables`/`.dump`/スナップショットのいずれにも
現れない（ExecDB自身のスキーマ列挙は`main`のみを見るため）——実データを
一切汚染しない。ただしTEMPオブジェクトは`pg_catalog.pg_class`のような
**スキーマ修飾付き**の名前では参照できない（`pg_catalog`という名の
データベースが実在しないため）。psqlODBCの`SQLTables`/`SQLColumns`クエリは
まさにこの修飾付き形式で送られてくるため、**クライアントから届いたSQL文の
`pg_catalog.`という文字列をサーバー側で単純に除去してから実行する**
（`rewritePGCatalogQuery`、`handleSimpleQuery`/`handleParse`双方に適用）
ことで、修飾あり/なしどちらの参照も同じTEMPオブジェクトへ解決させている。
SET/SHOW同様の「第3の区分」に近いが、こちらは文の一部を書き換えるだけで
実行自体はSQLiteへそのまま委ねる点が異なる。

**副次的に見つかったSQLite側の構文制約（`pg_type`）:**
`SELECT * FROM (VALUES (...), (...)) AS v(col1, col2, ...)`という、
派生テーブルに列名リストを付けるASエイリアス構文はSQLiteでは未対応
（`near "(": syntax error`）——標準SQL/Postgresでは通る書き方だが、
SQLiteの文法には無い。**対処: `pg_type`だけは`CREATE TEMP TABLE`+
`INSERT`の実テーブルにした**（VIEWではなくTABLE。他がVIEWなのは
`main`のライブスキーマを反映する必要があるからで、`pg_type`は固定の
型一覧なので実テーブルで問題ない）。

**接続プール再利用によるべき等性の問題（Npgsqlの`decodeBinaryParam`修正時と
同種の落とし穴）:** `engine.DB.Session`は`database/sql`の標準コネクション
プールから物理コネクションを配る（`engine/engine.go`）。TEMPオブジェクトは
論理的な`Session`ではなく物理コネクションの寿命に紐づくため、あるpgwire
接続が終了してプールへ返却された物理コネクションを、**別の新しいpgwire
接続が再利用**すると、その物理コネクション上には前回作成済みのTEMPビューが
既に存在し、`CREATE TEMP VIEW`が「already exists」で失敗する。
**対処: `sqlite_temp_master`を見て、`pg_type`が既に存在すればセットアップ
自体をスキップする**（`pgCatalogAlreadyAttached`）べき等な設計にした。
2本以上のpsqlODBC接続を同一サーバーに対して行って初めて発覚した
（1本目の接続だけをテストしている限り気づけない、というのがこの種の
バグの共通した性質）。

**Postgres組み込み関数の追加登録（`engine.RegisterScalarFunction`、新規）:**
`current_schema()`・`pg_get_expr(pg_node_tree, oid)`はSQLite本体には
存在しない関数のため、`modernc.org/sqlite.RegisterDeterministicScalarFunction`
経由でプロセス全体に対し1回だけ登録する必要があった。この登録APIは
`modernc.org/sqlite/lib`（`Complete`が使う生成コード）ではなく、
安定版の`modernc.org/sqlite`トップレベルパッケージが公開している
ドキュメント化されたAPIのため、`.claude/rules/sqlite-quirks.md`の
「`lib`は生成コードでバージョン間の破壊的変更リスクがある」という注意書きは
こちらには当てはまらない。`engine.RegisterScalarFunction`という薄い
ラッパーを新設し（`engine/function.go`）、関数名・実装（Postgres固有の
語彙）自体は`cmd/execdb/pgcatalog.go`側に置くことで、`engine`パッケージ
自体はPostgresの存在を知らないという既存の境界を保っている。
`current_schema()`は常に`'public'`を返し、`pg_get_expr()`は常に`NULL`を
返す（`pg_attrdef`ビューが常に0行なので、実際にはNULL引数でしか呼ばれない
——デフォルト値の式デコードという本来の機能は実装していない）。

**発見の副産物（`pg_attribute`に列が足りなかった）:** `SQLColumns`の実クエリを
そのままREPLで再現して初めて、`a.atthasdef`という列を`pg_attribute`に
用意し忘れていたことが判明した（`no such column: a.atthasdef`）。実際の
クエリ文をそのまま流して確認する、という一貫した手法がここでも効いた。

**スコープの位置づけ:** 制約・インデックス・トリガー・複数スキーマ
（ExecDBは`public`相当のスキーマ1つのみ）は対象外。あくまで
「基本的な接続・型付きクエリ・ドライバ自身のスキーマブラウザ
（SQLTables/SQLColumns）が実スキーマに対して動く」ことがゴールであり、
本格的なPostgresシステムカタログの再現ではない
（`.claude/rules/pgwire.md`全体の「サブセット、フルセットではない」
という方針のまま）。

## PHP・Ruby・Rust対応、および`Describe`/`Execute`のOID不整合バグ発見（フェーズ④完了後の追加）

ODBC対応の後、続けてBash（`psql`）・PHP・Ruby・TypeScript・Rustへの対応可否を
検討した。**Bash（`psql`）とTypeScript（node-postgresと同一パッケージ）は
既存カバレッジと完全に重複するため見送った。** PHP（PDO_PGSQL）・Ruby
（`pg` gem）は内部的にpsycopg2と同じ`libpq`をラップしているが、**PDOは
デフォルトで"エミュレートされたprepare"（パラメータをクライアント側で
文字列へ埋め込みSimple Queryとして送る）を使う**という、他のどのドライバとも
異なる経路を通るため追加する価値があると判断した。Rustの`postgres`/
`tokio-postgres`クレートは独自にワイヤープロトコルを再実装しており
（libpq非依存）、実機検証の結果、他の7ドライバでは見つからなかった
**2つの独立したExecDB側のバグ**を発見した。

### バグ1: `ParameterDescription`のOID 0への無限再帰（tokio-postgres）

tokio-postgresのデフォルト（型を指定しない）`query`/`execute`系APIは、
パラメータの型を自己申告せず、サーバーの`ParameterDescription`応答に
依存する。ExecDBが返す「未指定」を意味するOID 0を受け取ると、
tokio-postgresは**OID 0が実際に何の型かを`pg_catalog.pg_type`/
`pg_range`/`pg_namespace`へ問い合わせて解決しようとする**。OID 0は
実在しないため0行が返り、tokio-postgresは**同じ問い合わせを再度発行し、
これが無限に繰り返されクライアントのスタックオーバーフローに至る**
（実機確認済み）。

**対処: テストコード側で`prepare_typed`を使い、パラメータの型を
事前に自己申告する**（pgJDBC/Npgsqlが既定で行っているのと同じ手法）。
ExecDB側でグローバルな「未指定パラメータのデフォルトOID」を0からtext(25)
へ変更する案も試したが、これは**Rust側の型チェックの厳格さと衝突する**
（サーバーがtextと宣言した列に対し、Rustの`i32`のようなネイティブ型を
バインドしようとすると`WrongType`エラーになる——他5ドライバはOID 0の
ままで正しく動いているため、この案は不採用・revertした）。

### バグ2: `Describe`と`Execute`で列の型（OID）が食い違う（ExecDB本体の潜在バグ、全ドライバに影響しうる）

上記の対処後も、`SELECT $1`（`$1`をint4として`prepare_typed`宣言）が
`error deserializing column 0`で失敗する問題が残った。原因はExecDB本体の
既存の潜在バグだった——`handleExecute`（`pgextended.go`）が、**事前の
`Describe`が既にクライアントへ送った`RowDescription`とは無関係に、
実行時の実際のクエリ結果から`columnOID()`を再計算していた**。
tokio-postgresは「文レベルの`Describe`だけを行い、ポータルレベルの
`Describe`は行わない」という（Postgresプロトコル上正当な）呼び出し
順序を使うため、この2つの計算結果が食い違う場面が初めて表面化した——
文レベルの`Describe`はNULL仮バインドで列をtext(25)と判定して
`RowDescription`で約束したのに対し、`Execute`は実際にBindされた値
（int4）から再計算しint8(20)・バイナリ形式でデータを送ってしまい、
**クライアントが約束された型と実際のバイト列の不整合により値を
正しくデコードできなくなっていた**。

**対処: `Describe`が確定させたOIDを`preparedStatement.resultOIDs`/
`portal.resultOIDs`にキャッシュし、`Execute`はそれを再利用する**
（バイナリ/テキストの判定だけは`Execute`時点で確定する`resultFormats`を
使って都度計算し直す——`Describe`時点ではまだBindが起きておらず
フォーマット要求が存在しないため）。**このバグは他の7ドライバでは
一度も表面化しなかった**——pgJDBC/Npgsql/node-postgres等は基本的に
ポータルレベルの`Describe`も行う、または値取得側が緩い（文字列を
暗黙変換する）ため実害が無かった。tokio-postgresの型チェックの厳格さが
初めてこの不整合を可視化した、という位置づけ。**この修正はドライバを
問わず適用される、pgwire実装全体の正しさに関わる修正**である。

副次的に、`SELECT $1`のような「プレースホルダそのものが結果列になる」
問い合わせについて、文レベル`Describe`の仮バインドをNULLから
「宣言されたパラメータ型に応じた代表値」（int4→`int64(0)`、text→`""`等、
`representativeParamValues`）に変更した——宣言済みの型があるにも
かかわらずNULLを仮バインドすると`columnOID`のScanTypeフォールバックが
text(25)に落ちてしまうため。加えて`decodeBinaryParam`に`text`型の
ケースを追加した（PostgresのtextはPostgresの"バイナリ"形式も生UTF-8
バイト列そのものであり、tokio-postgresはデフォルトであらゆる
パラメータをバイナリ形式で送るため、text宣言のパラメータでも
バイナリ経路を通る）。

## 型マッピング（確定、フェーズ④Step 1〜2）

SQLiteは動的型付け（型アフィニティ）のため、列の値の型が固定されない。
`RowDescription`で返すPostgres型OIDは、**列の宣言型（`decltype`）ベースの
アフィニティ判定を第一優先とし、宣言型が空（式・集約・リテラル列）または
SQLite標準5分類の「NUMERIC」catch-allに落ちる場合のみ、先頭行を試験実行して
得た実際のGo動的型（`ScanType()`）にフォールバックする**という設計で確定した
（決め打ちで実装せず、実際に主要ドライバ——pgx/psycopg/pgJDBC/node-postgres——
で接続検証した結果。確定した対応表は`execdb_spec.md`§8参照、実装は
`cmd/execdb/pgtype.go`の`columnOID`/`affinityOID`/`scanTypeOID`）。

NUMERIC親和性の宣言型（`NUMERIC`/`DECIMAL(10,2)`等）をあえてフォールバック側に
回しているのは意図的な設計判断——SQLiteのNUMERIC親和性は内部的に必ずINTEGERか
REALのいずれかで格納され真の任意精度十進数を持たないため、Postgresの複雑な
バイナリNUMERIC形式（base-10000桁グループ符号化）の実装を丸ごと回避できる。

**フェーズ②Step 1実測済みの前提（実装に活用）:** `sql.Rows.ColumnTypes()`
は`modernc.org/sqlite`で`Next()`呼び出し前でも`DatabaseTypeName()`/`ScanType()`/
`Nullable()`が正しい値を返すことを実測確認済み（`INTEGER`/`REAL`/`TEXT`/`BLOB`/
`NUMERIC`列、および式列——`dbType`は空文字列、`scanType`は`int64`——で確認。
詳細は`.claude/rules/sqlite-quirks.md`「`ColumnTypes()`は`Next()`呼び出し前でも
正しい値を返す」節）。「`Next()`前は不定かもしれない」と慎重になる必要は
なかった。

**値の実際のエンコードはOIDではなく`Scan`後の実際のGo動的型を見て行う。**
宣言型と実際の値の型が食い違う場合（型アフィニティに違反する値が入っている、
`BOOLEAN`列が実際には`int64`として返る等）があるため、OIDと値の型の対応を
過信しない設計にした（`pgtype.go`の`pgEncodeValue`のdocコメント参照）。

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

## `SET`/`SHOW`互換シム（フェーズ④Step 3）

pgJDBCは接続直後に`SET extra_float_digits = 3`のようなセッションパラメータ
設定コマンドを自動的に送ってくる。SQLiteには`SET`/`SHOW`という文自体が
存在しないため、そのまま内部SQLエンジンへ渡すと構文エラーになり接続が
確立できない（`checkExternalAccess`の拒否対象にも入っていなかったため、
拒否ですらなく素通しして壊れる、というのが発見時の状態だった）。

`SET`/`SHOW`は`access.go`のDDL/DML分類とは別の、**外部I/F層（pgwireの
実装）が内部SQLエンジンへ渡す前に横取りする第3の区分**として扱う
（`cmd/execdb/pgsession.go`、`execdb_spec.md`§2/§8参照）。`access.go`
自体には手を入れない——「SQLiteへ渡す文の分類器」という役割を保つため。
`SET`には`CommandComplete("SET")`を、`SHOW`にはその値を1列1行の結果
（`ParameterStatus`と同じ値）として返し、実際にはSQLite側の設定を
一切変更しない。値は接続ごとの`sessionParams`（単純なマップ）に保持する。

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

## 実装時の気づき（フェーズ①Step 5、`pgx`実接続で確認。フェーズ④Step 2/5で解消済み）

- **`pgx`（Go）はデフォルトでExtended Queryプロトコル（`Parse`/`Bind`/
  `Execute`）を使う。** フェーズ①時点ではExecDBがSimple Queryのみ実装して
  いたため、何も指定しないと`conn.QueryRow`等の初回呼び出しで
  `unsupported message type 'P'`エラーになっていた。**フェーズ④Step 5で
  Extended Queryを実装したことで、この制約自体は解消済み**——現在は
  `default_query_exec_mode=simple_protocol`の指定なしでも`pgx`が接続できる
  （`tests/e2e.sh`は指定なし・指定ありの両方を回帰テストとして実行している）。
  ただし「デフォルト設定でSimple Queryを使うドライバ（`psql`等）だけで
  確認しても、Extended Queryが前提のドライバの互換性は保証されない」という
  教訓自体は今後も有効——新しいドライバを確認する際は、まずデフォルト設定で
  試すこと。
- **全列をOID 25(text)固定で返す実装（フェーズ①の暫定仕様）は、`pgx`の
  型チェックの厳しさによって実際に制約として顕在化していた。** `psql`は
  テキスト表示するだけなので気づきにくいが、`pgx`は`RowDescription`の
  型情報を厳格に見ており、text型の列を`*int`へ`Scan`しようとすると
  `cannot scan text (OID 25) in text format into *int`のようなエラーで
  拒否される（`*string`へのScanは常に成功する）。**フェーズ④Step 2で
  本来の型マッピングを実装したことで解消済み**（上記「型マッピング」節）。

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
