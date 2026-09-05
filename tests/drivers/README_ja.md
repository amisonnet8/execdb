# tests/drivers

*English version: [README.md](README.md)*

ExecDBのpgwire実装に対する、言語横断のドライバチェック(仕様書§8、
フェーズ4 Step 7)。`tests/pgclient`(Go、pgx)はすでにトランザクション
分離、`25P02`、切断/`CancelRequest`によるキャンセルまで含めてプロトコルを
深く検証している。ここでのチェックはそれとは違う、より狭い目的のために
存在する: **他言語の主要なPostgreSQLドライバ**が、可能な限りそれぞれの
デフォルト設定のままExecDBへ接続でき、基本的なSELECT/DDL拒否/
トランザクションのチェックがひと通り動くことを証明する——これがフェーズ4
の掲げるゴールと一致する。

| ディレクトリ | ドライバ | 必要なランタイム |
| :--- | :--- | :--- |
| `python/` | psycopg2 (`python3-psycopg2`) | `psycopg2`をimportできる`python3` |
| `node/` | node-postgres (`pg`) | `node` + `npm` |
| `java/` | pgJDBC | `java` + `javac`(初回実行時にMaven Centralからドライバのjarを取得) |
| `dotnet/` | Npgsql | `dotnet` SDK(初回実行時にNpgsqlのNuGetパッケージを復元) |
| `odbc/` | psqlODBC(pyodbc経由) | `unixodbc` + `odbc-postgresql`(ドライバ本体) + `pyodbc`をimportできる`python3` |
| `php/` | PDO_PGSQL(PDO経由) | `pdo_pgsql`拡張を読み込んだ`php` |
| `ruby/` | `pg` gem | `pg` gemをインストール済みの`ruby`(`gem install pg` / `gem install --user-install pg`) |
| `rust/` | `postgres`/`tokio-postgres` crate | `cargo`(初回実行時にcrates.ioからクレートを取得) |

**「デフォルト設定」の唯一の例外はNpgsql:** 接続文字列に
`Server Compatibility Mode=NoTypeLoading`が必要(`run-all.sh`が付与して
いて、`dotnet/Program.cs`にハードコードはしていない)——これが無いと
ExecDBへそもそも接続できない。理由は、Npgsqlの接続ブートストラップが
自分の型カタログを構築するために、Postgresシステムカタログ(`pg_type`、
`pg_enum`、...)に対する一連のSELECTと、素の`SELECT version()`を送って
くるため——SQLiteにはそれらが一切無いため、アプリケーションのクエリを
1つも送らないうちに、最初の接続試行自体が失敗する。これはワイヤー
互換だが本物のPostgresではないバックエンド(CockroachDB/Redshiftスタイル
のDBがNpgsqlユーザー向けに案内しているのと同じ)へ接続するための、標準の
Npgsqlネイティブなオプションであり、ExecDB固有のパッチではない。検証済み
の他のドライバは、どれもこの種のクライアント側フラグを必要としない。

**psqlODBCはクライアント側のフラグは不要だが、接続できているのは
ExecDBサーバー側の作業のおかげでしかない。** その接続ブートストラップは
本物のPostgresシステムカタログ(`pg_type`、ラージオブジェクト対応の
有無をチェック)へクエリを送り、その`SQLTables`/`SQLColumns`呼び出し
(`odbc/check.py`が使う、また実際のスキーマブラウズを行うODBC利用者
——Excel/Power BI/Access等——も使う)は`pg_class`/`pg_namespace`/
`pg_attribute`/`pg_attrdef`と、`pg_get_expr()`/`current_schema()`という
組み込み関数に対してクエリを送る。ExecDBはこれらすべてに、pgwire接続
ごとに1回セットアップする、pg_catalog互換の小さなTEMPビュー/関数の
集合で応答する——正確に何を(そして何を)エミュレートしていないかは
`cmd/execdb/pgcatalog.go`のdocコメントを、なぜそうなったか、なぜ
ビューを本物の`ATTACH`した`pg_catalog`データベースの中に置けなかったか
は`.claude/rules/pgwire.md`を参照。

**PHP(PDO_PGSQL)とRuby(`pg` gem)は、psycopg2と同様、libpqの薄い
ラッパーである**——実際のワイヤープロトコルの処理は3つとも同一のC
コードなので、その面では新しいプロトコルカバレッジを追加するわけでは
ない。それでも、libpqの*上*に何が乗っているかという点で、それぞれ
独自のチェックを持つ価値がある: PDO_PGSQLはデフォルトで「エミュレート
されたprepare」(このドライバでは`PDO::ATTR_EMULATE_PREPARES`が
デフォルトでtrue)を使う——パラメータの値をクライアント側でSQLテキストへ
埋め込み、結果をプレーンなSimple Queryテキストとして送る、他の検証済み
ドライバ(すべてデフォルトで本物のExtended Queryパラメータバインディング
を使う)とは実質的に異なるコードパスである。Rubyの`pg` gemは
`exec_params`経由で本物のExtended Queryバインディングを使い、psycopg2
自身の特有の使い方とは独立にそのパスを検証する。

**Rustの`postgres`/`tokio-postgres`crateは、ワイヤープロトコルの
本物の独立した再実装だ**(libpqを一切使わない)。その厳格で静的型付けの
APIサーフェスは、他の7つのドライバが見つけたどのバグとも別の、2つの
本物のExecDBバグを表面化させた:

1. デフォルト(型を意識しない)の`query`/`execute`メソッドはパラメータの
   OIDを未指定のままにし、サーバーの`ParameterDescription`での解決に
   頼る。0(ExecDBの「未指定」の答え——他のどのドライバも許容する)を
   告げられると、0が一体何の型なのかを`pg_catalog.pg_type`/`pg_range`/
   `pg_namespace`へ問い合わせにいく——そんなものは存在しないので、
   何度でも問い合わせを繰り返し、ついにはスタックオーバーフローする。
   `rust/src/main.rs`は`prepare_typed`を使って各パラメータの型を事前に
   自己申告することでこれを回避している(pgJDBC/Npgsqlがすでに暗黙的に
   行っているのと同じこと)——本物のRustアプリケーションが必要とする
   のと同じ対処である。
2. *statement*(*portal*ではなく)だけを常にDescribeするクライアント
   ——tokio-postgresがデフォルトで使う、有効かつプロトコル上正当な
   メッセージ順序——が、本物の不整合を露呈させた: `Execute`が、事前の
   `Describe`が`RowDescription`ですでに約束していた型を再利用せず、
   実行時の実際のクエリ結果から結果列の型を再計算していたため、両者が
   正当に食い違いうる状態になっていた(NULL仮バインドで推測した型と、
   実際にBindされた値から得た型の違い)。ExecDBは現在、Describe時点の
   OID(`preparedStatement`/`portal`の`resultOIDs`、
   `cmd/execdb/pgextended.go`)をキャッシュして再利用するよう修正
   済み——これはこのドライバだけでなく、すべてのドライバに恩恵のある
   修正である。

両方とも`.claude/rules/pgwire.md`に詳しく書き起こしてある。

各チェックは接続し、型付きの`SELECT`をいくつか実行し、DDLがSQLSTATE
`42501`で拒否されることを確認し、テーブル`t(a INTEGER)`(`tests/e2e.sh`が
`tests/pgclient`向けにシードするのと同じテーブル)に対してINSERT+COMMIT+
SELECTの往復を1回行う。成功すると、それぞれ`tests/pgclient`自身の慣習に
倣って標準出力へ`OK`を表示する。

## なぜこれらは`go test`/`make check`に組み込まれていないのか

これらのランタイムはどれもExecDB自身のビルドの一部でも、必須の開発者
ツール(`.claude/rules/testing.md`)でもない——フェーズ1の`psql`と同様、
必要になった時点でインストールするものである。`tests/e2e.sh`は各チェック
を、そのランタイムが`PATH`上に存在するときだけ実行し、無ければ`skip -`行
を表示する——これは既存のPTY専用Ctrl+Cチェックが、`script`が未インストール
のときに行う挙動と全く同じである。これは、Nodeの依存(`pg`)とJavaの
ドライバjarが、コミットされる代わりにgitignore済みの場所
(`node/node_modules/`、`java/lib/`)へ取得される理由でもある——
リポジトリへバイナリをコミットしないという`.claude/rules/distribution.md`
の方針と一貫している。.NETパッケージ(Npgsql)は`dotnet`によって、
リポジトリの外にある独自のNuGetキャッシュ(`~/.nuget/packages`)へ
復元される——gitignoreが必要なのは、プロジェクトごとの
`dotnet/bin/`/`dotnet/obj/`というビルド出力ディレクトリだけである。Rustの
クレート(`postgres`)も同様に`~/.cargo/registry`の下にキャッシュされる
——gitignoreが必要なのは`rust/target/`(ビルド出力)だけであり、
`rust/Cargo.lock`自体は`go.sum`と同様、再現可能な依存グラフのために
コミットする。PHPの`pdo_pgsql`拡張とRubyの`pg` gemは、それぞれシステム
パッケージ・ユーザーインストールしたgemであり、gitignoreすべきローカルな
ビルド出力自体が存在しない。
