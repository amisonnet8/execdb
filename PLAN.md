# PLAN

ExecDBの実装計画・進捗管理ドキュメント。実装が進むにつれて随時更新すること
（特に「現在地」「保留事項」は、セッションをまたぐたびに参照・更新する）。

## 開発フェーズ

実装は以下の4フェーズで進める。各フェーズとも、まずLinuxで開発し、完成後に
Windows/macOSや複数CPUアーキテクチャでの動作確認（GitHub Actions等）を行う
サイクルを繰り返す（詳細は `.claude/rules/testing.md` 参照）。

1. **①ミニマム実装（技術検証フェーズ）**: `engine`ライブラリ・REPL・
   PostgreSQL互換ワイヤープロトコル（TCP/UNIX Domain Socket）の3要素を、
   最小限の機能で薄く繋げて実装し、全体が技術的に成立するかを確認する。
   網羅性は求めない。`.overwrite`（自己上書き）もこのフェーズに含める
   （一番ハック的な部分であり、早期に全体の中で動作確認する価値が高いため）。
   接続確認はGoに加え、他言語ドライバも1つ程度試す。
2. **②`engine`ライブラリ開発**: ①の土台の上で本格的に作り込む。
3. **③REPL開発**: REPLコマンド体系を本格的に作り込む。
4. **④PostgreSQL互換ワイヤープロトコル開発**: Goでの実装を完成させてから、
   他言語ドライバでの動作確認を行う。

## フェーズ①のステップ

技術検証フェーズであるフェーズ①は、以下のステップで進める（2026-09-03、
計画レビュー済み）。各ステップの詳細（作成ファイル・API設計・動作確認手順）は
実装時のセッションログを参照。ここでは全体像と、ステップ内で確定した仕様変更の
反映先のみを記す。

1. **Step 1: 足場固め＋技術検証【意思決定ゲート】** — `modernc.org/sqlite` の
   取得、`Serialize`/`Deserialize` API の実シグネチャ・接続共有モデル
   （`file:execdb?mode=memory&cache=shared` + keeper接続）・復元後の書き込み
   可否をスパイクテストで実測し、仕様書§7の「暫定・未確定」を確定させる
   （NGならSQLダンプ方式へフォールバック）。`Makefile` 作成、
   ルート直下の `execdb_poc(POC実装).go` 削除もここで行う。
2. **Step 2: `engine` パッケージ** — フッターI/O、`DB` 型のSQL実行API、
   `Snapshot`/`SnapshotTo`/`Overwrite`/`Load`/`OpenSelf`。
3. **Step 3: `cmd/execdb` — CLI・バナー・REPL** — 起動オプション（`-u`と
   `-i`は先送り）、ドットコマンド（`.dump`等フェーズ③のものは先送り）、
   `.overwrite`の実機確認。
4. **Step 4: pgwire（TCP＋UDS）＋アクセス制御** — 自前実装（Simple Query
   サブセットのみ）。型マッピングは全列text暫定。ATTACH/PRAGMA/VACUUM等も
   DDLと同様に拒否。
5. **Step 5: E2E自動化・他言語ドライバ確認・CI・ドキュメント更新** —
   `tests/e2e.sh`、`tests/pgclient`（pgx、tests専用依存）、
   `psql`での疎通確認、GitHub Actions 3OSマトリクス、仕様書・ルールファイル
   への確定事項の反映。

**この計画で確定した方針:**

| 論点 | 決定 |
| :--- | :--- |
| pgwire 実装方式 | 自前実装（標準ライブラリのみ）。コーデック層を分離しpgproto3への退避経路を残す |
| ルートの `execdb_poc(POC実装).go` | 削除（内容は初回コミット `2cef9f8` の履歴に残る） |
| 外部I/F の ATTACH/PRAGMA/VACUUM | まとめて拒否。仕様書§2に「その他（外部I/Fでは常に拒否）」区分を追加 |
| 自動フック | `make build` を `PostToolUse`(Edit\|Write) で実行 |

**Step内で仕様書に反映する確定事項（該当ステップで実施）:**

| 反映先 | 内容 |
| :--- | :--- |
| §2 | 「その他（外部I/Fでは常に拒否）」区分を新設しATTACH/DETACH/PRAGMA/VACUUM/REINDEXを列挙（Step 4） |
| §6 | `db.Snapshot()` → `db.Snapshot(path string)` に確定／`engine.OpenSelf()` を追加（Step 2） |
| §7 | フッターの整数はbig-endianと明記／「エンジン部分をキャッシュ」はオフセットのみ保持の意味に修正／採用DSNを追記／Serialize方式の暫定表記を確定（Step 1・2） |
| §4 | `.load` のバージョン不一致警告は呼び出し側の責務（Step 2） |
| §10 | バナーの出力先はstderr（Step 3） |
| naming.md | 対応表に `engine.OpenSelf()` / `engine.Open(path)` の行を追加（Step 2） |

**フェーズ①の意図的なスコープ外（フェーズ②〜④で拾う）:** 型マッピング
（全てtext OID 25の暫定）、`--user`認証、`--snapshot-interval`、
`.import`/`.dump`/`.mode`/`.headers`、Extended Queryプロトコル、`.load`中の
既存pgwireセッション維持、`ExecContext`/`QueryContext`、`Begin`/`BeginTx`。

**フェーズ①完了の判定:** `make check`・`make test`が通る／`psql`と
`tests/pgclient`（pgx）の両方から疎通しDDL系が42501で拒否される／
GitHub Actions 3OSマトリクスがgreen（Windowsでの`.overwrite`含む）／
`go install`で入れたバイナリでも`.snapshot`/`.overwrite`が機能する。

## Step 1 で確定した事実（`Serialize`/`Deserialize`、2026-09-04実測）

仕様書§7は「暫定・未確定」としていたが、`engine/serialize_spike_test.go`
（`go test ./engine/ -run TestSpike -v`）で①〜④すべて実測しPASS。
**Serialize方式を確定**（フォールバック不要）。仕様書の想定と異なっていた点は
以下の通り（§7へ反映予定）:

- 実シグネチャは `func (c *conn) Serialize() ([]byte, error)` /
  `func (c *conn) Deserialize(buf []byte) error`。仕様書が書いていた
  `Serialize("main")`/`Deserialize("main", data)` の**スキーマ引数は存在しない**
  （常にmainスキーマ固定）。`conn`型はunexportedだが、メソッドはexportedなので
  `sql.Conn.Raw()` + ローカルinterface（`interface{ Serialize() ([]byte, error) }`等）
  でのアサーションで到達可能。
- ライブラリ内部で`Deserialize`時に既に
  `SQLITE_DESERIALIZE_RESIZEABLE|SQLITE_DESERIALIZE_FREEONCLOSE`
  を指定済みのため、復元後のDBは拡張可能（2万行追加INSERTで確認）。
  仕様書が懸念していた「復元専用DBになる」リスクは**該当しない**。
- 接続共有: `file:execdb?mode=memory&cache=shared` + keeper接続（Close()まで
  保持する`*sql.Conn`）で、複数の独立した`*sql.Conn`が同一インメモリDBを
  共有できることを確認（**ただしこれは通常のSQL文について。Deserializeには
  適用されない点をStep 2で追加発見、下記参照**）。
- ラウンドトリップ: TABLE/INDEX/VIEW/TRIGGER/AUTOINCREMENT/BLOBすべて
  Serialize→Deserializeで保持されることを確認。

## Step 2 で確定した事実（重要な追加発見、2026-09-04）

**`Deserialize`は呼び出したコネクション自身にしか反映されない。** 共有キャッシュ
DSN（`file:execdb?mode=memory&cache=shared`）を使っていても、`Deserialize`実行
「後」に新規に開いた別コネクションですら、その内容を見ることができない
（`engine/serialize_test.go` の `TestDeserializeDoesNotPropagateToOtherConnections`
で実測・固定化済み）。これはStep 1のスパイクでは検証していなかった組み合わせ
（Step 1は「共有キャッシュ＋通常SQL」と「Deserialize＋単一コネクション」を
別々にしか検証しておらず、「共有キャッシュ＋Deserialize」の組み合わせは
未検証だった）。

- **engine側の対処:** `DB.Exec`/`Query`/`QueryRow` は全て `db.sdb`（コネクション
  プール）ではなく `db.keeper`（`Open`/`OpenSelf`/`Load`が`Deserialize`を実行した
  のと同じ単一の`*sql.Conn`）経由に統一した。`Load`も新しいDBを丸ごと作り直す
  （新しい`*sql.DB`+`*sql.Conn`を用意し、その新しいコネクション上で
  `Deserialize`してから`db.keeper`を差し替える）ことで一貫性を保っている。
- **フェーズ③/④への影響（要検討事項として申し送り）:** 仕様書§2/§8が要求する
  「REPLと外部I/Fが2つの独立したクライアントとして同一DBを操作する」を
  `cmd/execdb`で実現する際、**両方が素朴に別々の`*sql.Conn`を持つ設計のままだと、
  起動時`OpenSelf()`や`.load`で取り込んだデータをpgwire側のセッションが見えない
  事故が起きうる。** 対策候補（フェーズ③/④着手時に判断）:
  1. `cmd/execdb`側もすべてのSQL実行を`engine.DB`の`Exec`/`Query`経由に統一し、
     生の`*sql.Conn`を個別に取り回さない（トランザクション状態はアプリ層で
     管理する）。
  2. `engine`側に、追加コネクションが必要になった場合の安全な取得方法
     （例: SQLiteのBackup/Restore API経由でkeeperの内容を新規コネクションへ
     コピーする、等）を新設する。
  現時点（フェーズ①）では1の方針（`engine.DB`のAPI経由に統一）で問題なく、
  2は将来的に真の並行アクセス性能が必要になった場合の拡張候補として保留する。

## フェーズ②のステップ

`engine`ライブラリ開発フェーズは、以下のステップで進める（2026-09-04、計画レビュー済み）。
ゴールは「`engine`を複数の独立したクライアントを安全に同時に扱えるライブラリに作り直し、
それが実際に機能することをe2eで実証すること」（§2/§8の中核要件がフェーズ①では未達の
まま完了していたため）。

**計画時に判明した新事実（`modernc.org/sqlite v1.58.0`のソースを直接確認）:**

| # | 事実 |
| :-- | :--- |
| N-1 | オンラインBackup API（`NewBackup`/`NewRestore`/`Backup.Step`/`Finish`）が公開されている。`Raw()`＋ローカルinterfaceで到達可能 |
| N-2 | `Deserialize`が伝播しない理由が判明: 内部で匿名（名前なし）memdbストアとして開き直すため。DSNを変えても直らない設計上の必然 |
| N-3 | memdb VFSが使える（`file:/name?vfs=memdb`）。名前が`/`始まりならグローバルにストアを共有 |
| N-4 | memdbはshared-cacheと異なりSHARED/RESERVED/EXCLUSIVEロック＋busy handlerが効く（`_busy_timeout`が機能する） |
| N-5 | `Serialize()`はトランザクションを考慮しない。他セッションが未コミットtx中に呼ぶと壊れたイメージを出力しうる |
| N-6 | `Serialize()`は`(nil, nil)`を返しうる（巨大DBでのmalloc失敗時） |
| N-7 | `ResetSession`はトランザクションをロールバックしない → プール経由APIで`BEGIN`を投げてはいけない |
| N-8 | `ColumnTypeScanType`は存在するが現在行依存（`Next()`前は不定）。フェーズ④の型マッピングの要 |
| N-9 | memdbの実効サイズ上限は1GiB。仕様書§7の「2GB未満」は不正確 |

**この計画で確定した方針:**

| 論点 | 決定 |
| :--- | :--- |
| `cmd/execdb`の結線 | フェーズ②に含める。pgwire/REPLをSession APIに載せ替え、2クライアント同時`BEGIN`が混線しないことを`tests/pgclient`で自動検証するところまで |
| クエリキャンセルの範囲 | 接続切断のみ。`CancelRequest`/`BackendKeyData`レジストリはフェーズ④へ送る |
| スキーマ内省API | フェーズ③へ送る。②では`ColumnTypes()`の実測記録のみ |

**ステップ構成:**

1. **Step 1: スパイク【意思決定ゲート】** — `engine/session_spike_test.go`で
   backup APIによるコネクション分離モデル（第一候補memdb VFS、第二候補shared-cache）を
   実測（①③④⑥⑧が全PASSする基盤を採用）。ダメならフォールバックF1（`engine`側で
   直列化。公開APIはF1でも同一に保つ）。
2. **Step 2: 基盤置き換え** — DSN・backup経由の`Open`/`Load`・ロック再設計
   （`db.mu`はメタデータ専用と再定義）・`Snapshot`/`Overwrite`のシリアライズバリア・
   typed errors・空blobガード・`Close`冪等化。
3. **Step 3: `Session`とcontext API** — `engine/session.go`新設、`ExecContext`等の
   export、`LoadFrom(io.Reader)`。`Begin`/`BeginTx`/`TxStatus`は意図的に追加しない
   （SQL文としての`BEGIN`をSessionの専有接続にそのまま流す設計）。
4. **Step 4: 並行性テストと`-race`常設化** — `engine/concurrency_test.go`、
   `Makefile`の`race`ターゲット、CI（ubuntu/macos限定の別ジョブ、Windowsは対象外）。
5. **Step 5: `cmd/execdb`結線** — pgwireを1接続=1`Session`化、REPLもSession化
   （必須。N-7のため）、`ReadyForQuery`の`'I'/'T'/'E'`と`25P02`、クライアント切断時の
   クエリキャンセル。`tests/pgclient`拡張でトランザクション分離をGo側から自動検証。
6. **Step 6: 仕様書・ルール・PLANへの反映** — §2/§4/§6/§7/§8の乖離修正、
   naming.md／sqlite-quirks.md／pgwire.md／testing.mdへの追記。

**フェーズ②完了の判定:** `make check`/`make race`/`make test`が通る／2つのpgwire
クライアントが同時`BEGIN`〜`COMMIT`で混線しない（`tests/pgclient`で自動検証）／
pgwireセッション維持中に`.load`が成功しセッション側が新データを見られる／長いクエリ
実行中の切断でキャンセルされる／`ReadyForQuery`が`'I'/'T'/'E'`を正しく返す／
GitHub Actions 3OSマトリクス＋raceジョブ＋trivyがgreen／仕様書と`.claude/rules/`が
実装と一致している。

計画の全文は `/home/vscode/.claude/plans/step-step-step-crispy-graham.md` を参照
（セッションログにも詳細が残る）。

## フェーズ②Step 1で確定した事実（コネクション分離モデル、2026-09-04実測）

`engine/session_spike_test.go`（`go test ./engine/ -run TestSpike -v -race`、
`CGO_ENABLED=1`必須——`-race`はcgoを要求するため。通常の`make check`は
`CGO_ENABLED=0`のまま問題なく通る）で、計画時の候補2基盤を実測。

**決定: `memdb` VFS（`file:/name?vfs=memdb&_busy_timeout=N`）を採用。
`cache=shared`インメモリDSNは不採用（後述する致命的な欠陥のため）。**

### ゲート結果

| # | テスト | memdb | sharedcache |
| :-- | :--- | :--- | :--- |
| ① | Backupで生きたDBへ反映 | PASS | PASS |
| ③ | 他セッションtx中のbackup | PASS（BUSYで拒否） | PASS（LOCKED_SHAREDCACHEで即拒否） |
| ④ | セッション間tx分離 | PASS（busy_timeout分だけ待ってBUSY） | **FAIL（無制限ハング）** |
| ⑥ | 未コミットtx中のSerializeバリア | PASS（barrier系） | **FAIL（barrier自体が無制限ハング）** |
| ⑧ | contextキャンセル | PASS | PASS |

①③④⑥⑧が全てPASSしたのは`memdb`のみ。`sharedcache`は④⑥で失格。

### 決定的だった発見: `sharedcache`は「読み取りが無制限・キャンセル不能にハングする」

計画時点でN-4として「shared-cacheはbusy handlerが呼ばれない」と記録していたが、
実測するとこれは**「エラーが早く返る」ではなく「本当に無限に待つ」**という、
想定より遥かに深刻な欠陥だった。

- `modernc.org/sqlite`は共有キャッシュのテーブルロック衝突
  （`SQLITE_LOCKED_SHAREDCACHE`、拡張コード262）に対して、
  `sqlite3_unlock_notify`ベースの独自リトライ機構（`conn.go`の`retry()`）を
  実装している。この待機は生の`sync.Mutex.Lock()`であり、
  **`context.Context`のキャンセルも、DSNの`_busy_timeout`も一切見ない**。
  相手のトランザクションが未来永劫コミット/ロールバックされなければ、
  文字通り無限に待ち続ける。
- 素朴な単一ゴルーチンでの検証コード（`/tmp/waltest.go`、セッションログ参照）
  ではGoランタイム自身の「all goroutines are asleep」デッドロック検出が
  発火してプロセスごと落ちた。spikeテストでは、この危険な呼び出しを
  バックグラウンドgoroutine＋`select`+`time.After`（`spikeRunHardBounded`
  ヘルパー）で包み、メインのテストgoroutineを実行可能な状態に保つことで
  安全にハングを検出・記録できるようにした。
- 対照的に`memdb`の同じ衝突は**通常のSQLite内蔵busy-handler**を経由し、
  `_busy_timeout`が正しく効く（設定した秒数だけ待ってから`SQLITE_BUSY`で
  諦める、という有界な挙動を実測で確認）。`modernc.org/sqlite`のソース
  （`_memdbLock`、`lib/sqlite.go`）を読むと、`memdb`の書き込みロック
  （`FnWrLock`）は新規`SHARED`ロック取得もブロックする、という
  ファイルベースDBより粗い（が、有界で安全な）ロック粒度であることも判明
  （通常のロールバックジャーナルなら`SHARED`と`RESERVED`は共存できるが、
  `memdb`はより単純化された実装のため、新規読み取りも書き込み中は
  `_busy_timeout`の間だけ待たされる）。

### その他の実測結果

- **`journal_mode=WAL`はどちらの基盤でも効かない**（`PRAGMA journal_mode=WAL`
  実行後も`memory`のまま）。インメモリDB向けのWAL（真のMVCC、読み書き非
  ブロック）という第三の道は存在しない。
- **`Serialize()`は未コミットtx中に呼ぶと、`memdb`では明示的なエラー
  （`invalid length returned: -1`）を返す**（N-5で懸念した「壊れたイメージを
  静かに返す」ではなく、エラーで弾かれる、というより安全側の結果だった）。
  一方`sharedcache`では実際に未コミット行が漏れて`Serialize()`されることを
  確認（`integrity_check`は`ok`のまま、つまり壊れてはいないが一貫性のない
  スナップショットになる）。`BEGIN IMMEDIATE`バリアを噛ませれば、両基盤とも
  他セッションのtx中は正しく拒否され、コミット後は正しく確定データのみを
  含むスナップショットが取れることを確認（Step 2で採用するバリア設計の
  正しさを裏付け）。
- **`ColumnTypes()`は`Next()`呼び出し前でも正しい`DatabaseTypeName`/
  `ScanType`を返す**（N-8で懸念した「`Next()`前は不定」は杞憂だった。
  `INTEGER`/`REAL`/`TEXT`/`BLOB`/`NUMERIC`列、および式列（`dbType`は空文字、
  `scanType`は`int64`）を実測。フェーズ④の型マッピング設計にそのまま使える）。
- **`memdb`の実効サイズ上限は約960MiB付近で`SQLITE_FULL`**（"database or
  disk is full"）。`SQLITE_MEMDB_DEFAULT_MAXSIZE`（1GiB）に近い値で
  頭打ちになることを確認（N-9の裏付け）。対照的に`sharedcache`は1.1GiB超でも
  エラーにならず、`memdb`特有の制約であることが分かった
  （仕様書§7の「2GB未満」を「約1GiB」に訂正する必要がある）。
- `engine/session_spike_test.go`は決定ゲートの記録として残すが、
  `sharedcache`のハング検出箇所は`t.Errorf`ではなく`t.Logf`に変更した
  （採否は既に確定したため、以後の`make check`／CIを恒常的に赤くする
  必要はない。フェーズ①の`serialize_spike_test.go`と同様、後続ステップで
  整理・追加テストへの切り出しを行う想定）。

## 現在地

*（このセクションは実装が進むにつれて更新すること。「今どのフェーズの、
どの部分に着手しているか」を都度書き残しておくと、セッションをまたいだ
ときに文脈を復元しやすい。）*

- **フェーズ④（PostgreSQL互換ワイヤープロトコル開発）完了、
  かつ当初計画していた4フェーズ（①〜④）すべてが完了（2026-09-04）。**
  詳細は下記「フェーズ④（PostgreSQL互換ワイヤープロトコル開発）完了」節・
  「フェーズ④Step 8（仕様書・ルール・PLAN・devcontainer統合）完了」節を参照。
  フェーズ④完了後、ユーザーとの相談で`docs/`への再構成（Step 9）→
  `tour/`入門ガイド（Step 10）という別枠の作業を追加することに決定。
- **Step 9（`docs/`への再構成、`tour/`を除く）完了（2026-09-04）。**
  `execdb_spec.md`を`docs/spec/execdb_spec.md`へ移動（単一ファイルのまま、
  章立て・§番号は不変。ユーザー承認済みの方針——「CLAUDE.md/.claude/rules/等
  からの`execdb_spec.md§N`という節番号ベースの引用は移動後もそのまま通用する
  ので更新不要、パスを明記している箇所（`CLAUDE.md`の参照ファイル一覧、
  `directory-structure.md`のツリー図）だけ更新する」）。`docs/usage/`
  （CLIオプション・REPLコマンドのリファレンス、2ファイル）・`docs/examples/`
  （CI/CDテストDB・バグ再現共有・モックAPIサーバー・SQLサンドボックスの
  4本、実際に`bin/execdb`へ流し込んで出力を実測検証済み）を新設。
  `README.md`/`README_ja.md`を、開発初期のまま放置されていた
  「早期開発段階・動作するビルドなし」というプレースホルダから、
  実際に動くクイックスタート（`go install`→REPL起動→`.snapshot`→
  スナップショット実行、という一連の流れ）へ全面書き換え——全リンクは
  スクリプトで解決確認済み。`.claude/rules/directory-structure.md`の
  ツリー図・補足説明を新しい`docs/`構成に合わせて更新（`tests/`と
  `docs/examples/`の役割の違いも明記）。`make check`・`make test`とも green
- **フェーズ③Step 1（REPL基盤の再構築）完了（2026-09-04）。**
  - `cmd/execdb/access.go`: `splitStatements`から`scanStatements(sql) (complete []string,
    remainder string)`を切り出し（`splitStatements`はremainderが非空白なら末尾へ追加する
    薄いラッパーに変更、pgwire側の挙動は無変更）。REPLはこの`remainder`で文が完結したかを
    判定するため、`INSERT INTO t VALUES ('a;b');`のようなリテラル内`;`での誤分割や、
    `SELECT 1; SELECT 2;`を1行で打った際に2文目が黙って捨てられる不具合が解消した
    （実機確認済み）。`looksLikeRowReturning`も`repl.go`から`access.go`へ移設し、
    自前の前方一致判定をやめて`firstKeyword`ベースの実装に置き換え（コメント・
    複数行に対応）
  - `cmd/execdb/repl.go`: 全面書き換え。`db`/`sess`/`opts`を引き回す関数群を
    `type repl struct { db *engine.DB; sess *engine.Session; opts *options;
    interactive bool }`に集約し、`handleDotCommand`/`cmdTables`/`cmdSchema`/
    `cmdSnapshot`/`cmdOverwrite`/`cmdLoad`をメソッド化。Step 2以降（出力モード・
    Ctrl+C状態）が乗る土台として意図した変更
  - `cmd/execdb/dotcmd.go`（新規）: `parseDotCommand`/`splitDotCommandArgs`——
    sqlite3準拠のクォート付き引数パース（`'...'`は無エスケープでリテラル、`"..."`は
    `\`エスケープあり、クォート外の`\`はスペースを含める用途）。`strings.Fields`では
    扱えなかった`.import 'my data.csv' t`・`.snapshot "my db"`が通るようになった
    （実機確認済み）
  - **`.exit [CODE]` / `.quit [CODE]`を追加**（sqlite3準拠）。引数なしは従来どおり
    `run()`から正常return（deferによるクリーンアップを経て終了）、引数ありは
    `os.Exit(code)`で即座に終了する（ExecDBは終了時にフラッシュすべき状態を
    持たないため、クリーンアップ省略は安全と判断）
  - 新規テスト: `access_test.go`に`TestScanStatementsRemainder`・
    `TestLooksLikeRowReturning`、`dotcmd_test.go`（新規、クォート・エスケープ・
    不正な引用符のテーブル駆動テスト）。全PASS
  - `make check`・`make test`（e2e、全項目PASS）とも green を確認済み。加えて
    実機確認（`bin/execdb`直接実行）で、1行複文実行・リテラル内`;`の非分割・
    `.exit 7`の終了コード・スペース入りファイル名への`.snapshot`を確認済み
- **フェーズ③Step 2（出力フォーマット `.mode`/`.headers`）完了（2026-09-04）。**
  - `cmd/execdb/format.go`: 全面書き換え。`outputMode`型（`list`/`column`/`csv`/
    `json`/`line`の5種、box/markdown/html等の装飾系は`.claude/rules/cli-output.md`
    に従い不採用）。`repl.printRows`がモードごとのレンダラへディスパッチ
    （`printRowsList`/`printRowsCSV`/`printRowsLine`/`(r *repl) printRowsColumn`/
    `printRowsJSON`）。`column`モードのみ列幅算出のため全行をバッファリング
    （ExecDBはそもそもインメモリDB・約1GiB上限なので許容と判断）。`csv`は
    `encoding/csv`＋`UseCRLF=true`でsqlite3同様CRLF。`json`はキー順序を保つため
    `map[string]any`を経由せず手組み。**BLOB（`[]byte`型として判別——実測で
    TEXT列は`string`、BLOB列は`[]byte`として`database/sql`から返ることを確認
    済み）はjsonモードでのみ16進エンコード**（生バイト列をJSON文字列へ直接
    埋め込むと`encoding/json`が不正UTF-8をU+FFFDへ静かに置換しデータを壊す
    ため。list/csv/line/columnの各モードでは従来どおり生バイト列をそのまま
    文字列化——フェーズ①からの挙動を維持）。pgwire用の`formatValue`は既存の
    シグネチャのまま維持（外部I/Fは常にテキスト形式でOID型マッピングは
    フェーズ④）
  - `cmd/execdb/repl.go`: `repl`構造体に`mode outputMode`・`headers bool`
    フィールドを追加。`.mode MODE`・`.headers on|off`ドットコマンドを追加。
    **`.mode column`へ切り替えるとsqlite3同様`headers`が自動でonになる**
    （その後`.headers off`で個別に無効化可能）。`.help`にも追記
  - 新規テスト`format_test.go`: `captureStdout`/`captureStderr`ヘルパーで
    NULL・引用符入り文字列・BLOB・整数・実数を含む1つのフィクスチャに対し
    5モード×ヘッダ有無のゴールデンテストを実施、全PASS
  - `make check`・`make test`（e2e、全項目PASS）とも green を確認済み。
    実機確認（`bin/execdb`直接実行）で全5モードの出力を確認済み
- **フェーズ③Step 3（`.dump`）完了（2026-09-04）。**
  - `cmd/execdb/dump.go`（新規）: `.dump [PATTERN]`。`PRAGMA foreign_keys=OFF;`→
    `BEGIN TRANSACTION;`→（各テーブルの`CREATE TABLE`→そのテーブルの`INSERT`、
    `sqlite_master`の自然順）→（`sqlite_sequence`が存在し行があれば
    `DELETE FROM sqlite_sequence;`＋復元INSERT）→（`tbl_name`列でPATTERNに
    紐づくindex/view/trigger、`sql IS NOT NULL`で暗黙のautoindexを除外）→
    `COMMIT;`。**リテラル化はSQLite自身の`quote()`関数に委譲**（Go側でNULL/
    BLOB/TEXTを型スイッチで判別しようとすると、`Scan`後はBLOBの文字列化と
    TEXTが両方ともGoの`string`になり区別できないため。列名一覧は
    `pragma_table_info(?)`——バインドパラメータを受け付ける関数形式——で取得。
    識別子は`quoteIdent`で`"`エスケープ）
  - `cmd/execdb/repl.go`: `.dump`ディスパッチ・`.help`追記
  - 新規テスト`dump_test.go`: TEXT/BLOB/NULL/REAL/AUTOINCREMENT/INDEX/VIEW/
    TRIGGERを含むDBで`.dump`し、出力を空DBへ`sess.Exec(dump)`（1回のExec
    呼び出し）で流し込むラウンドトリップテスト、および`PATTERN`引数で
    対象テーブルを絞るテスト。全PASS
  - e2eに`.dump`→別プロセスのREPLへパイプ→`SELECT`で確認するチェックを追加
    （トリガーを含まないスキーマで検証——理由は下記の発見を参照）
  - `make check`・`make race`・`make test`（e2e、全項目PASS）とも green
  - **副産物として、2つの実装上の発見があった:**
    1. **REPLの行ベース文スキャナ（Step 1の`scanStatements`）は
       `CREATE TRIGGER ... BEGIN ...; ... END;`のような複合文本体の内部`;`を
       理解できず、そのままREPLへ手入力・ペーストしても`SQL logic error:
       incomplete input`で壊れることを確認した（`.dump`固有の問題ではなく、
       Step 1で作った文スキャナ自体の既存の制約——`quote()`/リテラル/
       コメントは理解するが、BEGIN...END複合文構造は理解しない）。
       本家`sqlite3`はこれを`sqlite3_complete()`という専用のトークナイザで
       解決しているが、同等の実装はナイーブな文字列ベースのBEGIN/END深さ
       カウントでは「CASE式のEND」との衝突などの誤検出リスクがあり、
       安全に実装するには相応の設計が必要と判断。**フェーズ③の計画外の
       スコープ拡大を避けるため、Step 3では対応せず既知の制限として
       ここに記録する**（`.dump`自体のSQL生成が正しいことは
       `sess.Exec()`への一括流し込みで検証済み——問題は`.dump`の出力側
       ではなく、REPL入力側の受け取り方にある）。トリガーを含むDBを
       REPL標準入力へ直接ペースト・パイプする運用は、この制約を踏む
       可能性がある。**→ ユーザー指示によりStep 4として新規追加し解決済み
       （下記「フェーズ③Step 4」参照）。**
    2. **`gofmt -s`（Go 1.26.7で確認）は、宣言直前のdocコメント内に隣接する
       `''`（アポストロフィ2つ）があると、これを単一の右巻きクォート文字
       `”`（U+201D）へ書き換える。** これは`go/doc/comment`パッケージによる
       doc comment整形の一部で、宣言に紐付かない通常のインラインコメント
       （関数内の一行コメント等）には影響しない。SQL の `''`
       エスケープ規則を説明するdocコメントを書く際に踏みやすい
       （`dump.go`のコメントで実際に踏んだ）。**回避策: docコメント内で
       `''`を隣接させず、単語で説明する**（本セッションでは実施済み）。
       このプロジェクトの`fmt-check`は`gofmt -s -l .`を使うため、
       CIでも同じ書き換えが必要になる。**→ `.claude/rules/testing.md`に
       「`gofmt -s`のdocコメント整形時の落とし穴」節として追記済み**
       （新規ファイルではなくtesting.mdを選択——既存の「GitHub Actions
       CI設定時の落とし穴」節と同種の「ビルド・CIツールチェーンの挙動を
       実測して記録する」カテゴリであり、単発の狭い気づきのために新規
       ファイルを作る基準には満たないと判断）
- **フェーズ③Step 4（REPLの複合文対応・`sqlite3_complete`統合）完了
  （2026-09-04）。** Step 3で見つかった「REPLの文スキャナがCREATE TRIGGERの
  BEGIN...ENDを理解できない」制限への対応（ユーザー指示により計画へ新規
  追加。当初計画のStep 4以降は5/6/7へ繰り下げ）。
  - `engine/complete.go`（新規）: `Complete(sql string) (bool, error)`。
    `modernc.org/sqlite/lib`（`Xsqlite3_complete`）＋`modernc.org/libc`
    （`NewTLS`/`CString`/`Xfree`）経由で本家`sqlite3` CLIと同じ
    `sqlite3_complete()`をそのまま呼ぶ。ナイーブなBEGIN/END深さカウントは
    CASE式のENDと衝突するリスクがあるため不採用とし、本家のトークナイザに
    判定を委譲する設計とした（実測でCASE式とトリガー本体終端のENDを正しく
    区別することを確認済み——下記テストケース参照）。**新規の外部依存の
    追加ではない**（`modernc.org/libc`・`modernc.org/sqlite/lib`は
    `modernc.org/sqlite`自身の既存の推移的依存、`go.sum`変更なし）。
    `directory-structure.md`の境界に従い、`modernc.org/sqlite`内部への
    アクセスは`engine`パッケージに閉じ込めた
  - `cmd/execdb/complete.go`（新規）: `completeStatements(sqlText string)
    (complete []string, remainder string)`。`scanStatements`の
    quote/comment考慮済み候補境界（`;`の位置）を`engine.Complete`で
    1つずつ検証し直し、トリガー本体内の`;`を正しく除外する
  - `cmd/execdb/repl.go`: `run()`のループが`scanStatements`ではなく
    `completeStatements`を使うよう変更
  - 新規テスト: `engine/complete_test.go`（空文字列・コメントのみ・通常文・
    複数文・トリガーのCASE入り本体の完了/未完了、全PASS）、
    `cmd/execdb/complete_test.go`（`completeStatements`がトリガー本体を
    1つの文として返すことを確認）
  - e2eに2件追加: `.dump`のスキーマにTRIGGERを含めて別プロセスのREPLへ
    パイプし正しく再生されることを確認（Step 3では既知の制限としていた
    ケースがこれで解決）／トリガーをREPLへ複数行に分けて直接入力し、
    本体内に`CASE WHEN ... END`を含んでいても1つの文として正しく実行
    されることを確認
  - `.claude/rules/sqlite-quirks.md`に`modernc.org/sqlite/lib`への直接
    依存の性質（生成コードで公開APIの保証がない、バージョンアップ時の
    確認事項）を新節として追記。`.claude/rules/naming.md`の対応表に
    `engine.Complete(sql string) (bool, error)`（ライブラリ専用）を追加
  - `make check`・`make race`・`make test`（e2e、新規2件含め全項目PASS）
    とも green を確認済み。実機確認でトリガーの複数行入力（CASE式込み）が
    正しく1文として実行されることを確認済み
- **フェーズ③Step 5（`.import`）完了（2026-09-04）。**
  - `engine/session.go`: `Session.Prepare(query)` / `PrepareContext(ctx,
    query)`を追加（このフェーズ2件目のengine変更。Step 4の`engine.Complete`
    に続く）。`Session`の専有コネクション上でprepareした`*sql.Stmt`を返す
    薄いラッパーで、1行ごとの`Exec`による毎回prepareのコストを避ける。
    新規テスト`TestSessionPrepare`——prepare済み文が後から張った
    トランザクション（`BEGIN`/`ROLLBACK`）にも正しく追従することを確認
    （`Session`が専有コネクションであることの裏付け）
  - `cmd/execdb/import.go`（新規）: `.import FILE TABLE`。**常にCSVとして
    読む**（`.mode`には連動させない——sqlite3との意図的な相違、仕様書§3に
    明記）。テーブル未存在なら1行目を列名として全TEXT列で自動`CREATE`、
    存在すれば1行目からデータとして扱う。投入は`SAVEPOINT execdb_import`
    で囲む（素の`BEGIN`ではなくSAVEPOINTにしたのは、ユーザーが既に`BEGIN`
    済みの状態で`.import`しても壊れないようにするため）。**フィールド数が
    列数と不一致の行があれば行番号付きエラーで処理全体を中断し、1行も
    投入しない**（sqlite3は警告して継続するが、シードデータ投入という
    主用途では歪んだデータが黙って入る方が有害と判断——意図的な相違、
    仕様書§3に明記）
  - `cmd/execdb/repl.go`: `.import`ディスパッチ・`.help`追記
  - 新規テスト`import_test.go`（6件、全PASS）: ヘッダからの自動CREATE・
    クォート/改行入りフィールド・既存テーブルへの投入（1行目もデータ扱い）・
    フィールド数不一致時の全ロールバック・空ファイルでの新規テーブル
    エラー・**ユーザーが既に開いていたトランザクションが`.import`後も
    無事`COMMIT`できること**（SAVEPOINT設計の裏付け）
  - e2eに`.import`→`SELECT`のチェックを追加（クォート・カンマ入りフィールド
    含む）
  - 仕様書§3（`.mode`の5モード確定表記、`.import`/`.dump`の相違点明記、
    `.exit [CODE]`）・§6（`Session`に`Prepare`/`PrepareContext`追加）・
    `.claude/rules/naming.md`（対応表に`s.Prepare`/`s.PrepareContext`行を
    追加）を更新
  - `make check`・`make race`・`make test`（e2e、全項目PASS）とも green
    を確認済み。実機確認（クォート・改行入りCSV、既存テーブル投入、
    フィールド数不一致時のロールバック）もすべて成功
- **フェーズ③Step 6（`--snapshot-interval`とCtrl+C）完了（2026-09-04）。**
  フェーズ③最大リスクのステップ（並行性・シグナル・contextキャンセルが絡む）。
  - **`-i`/`--snapshot-interval`:** `options.snapshotInterval time.Duration`を
    追加。`cmd/execdb/main.go`の`run()`が`opts.snapshotInterval > 0`のとき
    バックグラウンドgoroutine（`runSnapshotInterval`）を起動、REPLモード・
    サーバーモードどちらでも有効（`defer close(stop)`で`run()`終了時に停止）。
    保存失敗（`engine.ErrBusy`等）はstderrへ出して次のtickへ進み、
    プロセスを落とさない。`cmdSnapshot`（`.snapshot`）・
    `autoSaveOnShutdown`（サーバーモード終了時）・`runSnapshotInterval`の
    「ベース名決定→`snapshotFilename`→`db.Snapshot`」を`resolveSnapshotFilename`
    ヘルパーに集約
  - **Ctrl+C:** `cmd/execdb/interrupt.go`（新規）に`replInterrupts`
    （sqlite3の`shell.c`と同じ状態機械——SIGINTのたびにカウンタを増やし、
    新しい入力行を読むたびにリセット。カウンタが2を超えたら即
    `os.Exit(1)`、1回目はクエリ実行中なら`cancel()`、アイドルなら
    「入力破棄」を通知するだけで終了しない）と`lineReader`
    （`bufio.Scanner`をバックグラウンドgoroutineで回し、メインループが
    stdin読み取りでブロックされないようにする——pgwireの
    `watchForDisconnect`と同型のパターン）を追加。**非対話（パイプ）時は
    シグナルハンドラを一切登録しない**——`idleSig`が`nil`チャネルのままに
    なることで`select`のその分岐が自然に無効化され、SIGINTの既定動作
    （即終了）がそのまま保たれる
  - `cmd/execdb/repl.go`: `run()`のメインループを`lr.lines`と
    `idleSig`（アイドル時のみ有効）の`select`へ再構成。`execSQL`が
    `context.WithCancel`＋`r.interrupts.begin(cancel)()`（deferで登録・
    解除）でクエリ実行をキャンセル可能にし、`sess.Exec`/`Query`から
    `sess.ExecContext`/`QueryContext`へ切り替え
  - 新規テスト`interrupt_test.go`（7件、全PASS）: 状態機械を
    `os.Exit`を実際には呼ばない`exitFunc`差し替えで直接テスト
    （アイドル1回目は継続・連続2回目で終了・`resetOnNewLine`後は
    フレッシュな1回目扱い・クエリ実行中はcancel優先・実行中でも連続
    2回目は強制終了・`end()`後はcancel解除）、`lineReader`の基本動作
  - e2eに2件追加: `--snapshot-interval`（サーバーモードで`snap1`を再利用、
    定期保存ログと保存内容を確認）、**`script`（util-linux）によるPTY確保
    Ctrl+Cテスト**（`command -v script`が無ければskip）——長いクエリ実行中の
    Ctrl+Cでキャンセルされセッションが使え続けること、アイドル時の
    1回目Ctrl+Cでは終了しないこと、連続2回目で終了コード1になることを
    実機確認
  - **e2e実装中に踏んだ`set -e`の落とし穴（新知見）:** パイプの右側を
    `{ timeout ... script ...; echo $? >file; }`という別ブロックで囲み
    終了コードをファイル経由で受け取る設計にしたが、**パイプの各段は
    別のサブシェル（フォークされた子プロセス）で実行され、`set -e`
    （errexit）はそのサブシェルにも継承される。** そのため、
    期待どおりの非ゼロ終了（今回は連続Ctrl+Cによる`exit 1`）自体が
    `echo $?`に到達する前にサブシェルを中断させ、ファイルへの書き込みが
    行われないまま外側のスクリプトが（EXITトラップ経由で）静かに
    終了してしまう、という分かりにくい失敗を引き起こした。**対処:
    そのサブシェルの先頭で`set +e`し、危険なコマンドの終了コードを
    errexitの対象から外してから`$?`を読む。** `.claude/rules/testing.md`
    へ追記
  - `make check`・`make race`・`make test`（e2e、3回連続実行でflakinessなし）
    とも green を確認済み。加えて`CGO_ENABLED=1 go build -race`した実バイナリ
    に対して実際のCtrl+C操作・`-i`＋SIGTERM操作を行い、データレースが
    無いことを確認済み（interrupt.go・lineReaderの並行設計が単体テストの
    枠を超えて実機でも安全であることの裏付け）
- **フェーズ③Step 7（仕様書・ルール・PLANへの反映）完了（2026-09-04）。**
  - `execdb_spec.md`: §10の`[4. 停止（REPLモード）]`を全面書き換え——
    Ctrl+Cが対話端末では終了ではなく中断コマンドであること（クエリ実行中は
    そのクエリだけ中断／アイドル時1回目は入力破棄のみ／連続2回目で終了
    コード1）、非対話時は既定動作のまま即終了すること、`.exit`/`.quit`/
    EOF（Ctrl+D）が通常の終了手段であることを明記。§4の「プロセス停止時の
    自動保存は行わない」の記述も、Ctrl+Cが常に即終了するわけではない旨の
    §10参照を添えて更新。§3（`.mode`5種・`.import`/`.dump`の意図的な相違・
    `.exit [CODE]`）・§6（`Session.Prepare`/`PrepareContext`）は
    Step 5で先行反映済みのため変更なし。§9（`-i`の表・定期スナップショット
    節）はすでに実装と一致していたため変更なし
  - `.claude/rules/cli-output.md`: 「出力モード」節を新設（`.mode`5種の
    採用理由と装飾系不採用の根拠、各モードの詳細、BLOBの型判別方法）。
    「SIGINTハンドラの登録可否」節を新設（対話時のみ登録する理由——
    非対話実行時にCtrl+Cで止められなくなる事故を防ぐため）
  - `.claude/rules/naming.md`: 対応表に`.mode`/`.headers`/`.dump`/`.import`
    （いずれもCLI起動オプション・`engine` APIの対応を持たないREPL専用
    コマンド）と`-i`/`--snapshot-interval`（`db.Snapshot`を`.snapshot`と
    共用）の行を追加。スキーマ内省APIを`engine`へ切り出さないという
    フェーズ③の確定方針も明記
  - `PLAN.md`:「フェーズ③・④への申し送り」を「フェーズ④への申し送り」へ
    整理——フェーズ③で片付いた項目（`.dump`/`.import`/`.mode`/`.headers`/
    `-i`/Ctrl+C）を除き、スキーマ内省APIは「不採用」と確定したことを明記。
    残るのは型マッピング・`--user`認証・`CancelRequest`・Extended Query
    （すべてフェーズ④スコープ、変更なし）
  - `make check`・`make race`・`make test`とも green を確認済み（ドキュメント
    のみの変更のため回帰リスクは無いが、念のため実行）
- **フェーズ③（REPL開発）完了。** 全7ステップ（REPL基盤の再構築→出力
  フォーマット→`.dump`→`sqlite3_complete`統合→`.import`→
  `--snapshot-interval`とCtrl+C→ドキュメント反映）を完了し、REPLは
  仕様書§3が宣言するコマンド体系をすべて実装した、sqlite3 CLIに準じる
  実用的な対話環境になった。次のアクションはフェーズ④
  （PostgreSQL互換ワイヤープロトコル開発）の計画立案。
- 下準備として以下を作成済み（2026-09-03時点）:
  - `go.mod`（module: `github.com/amisonnet8/execdb`）
  - `LICENSE`（MIT, copyright: amisonnet8）
  - `.gitignore`
  - `README.md` / `README_ja.md`
  - `tests/README.md`（用途の説明のみ、中身は未整備）
- Step 1で以下を実施済み（2026-09-04時点）:
  - `modernc.org/sqlite v1.58.0` を取得、`go.sum`確定。`trivy fs --scanners
    license,vuln .` でCVE・GPL系混入なしを確認済み
  - `engine/serialize_spike_test.go` でSerialize/Deserializeの実測検証を実施、
    全PASS（詳細は上記「Step 1で確定した事実」節）
  - `Makefile` 作成（build/run/unit/e2e/test/fmt/fmt-check/vet/lint/
    check-deps/tidy/check/clean/help）。`make check`（fmt-check+vet+
    check-deps+unit）が通ることを確認済み
  - ルート直下の `execdb_poc(POC実装).go` を削除（内容は初回コミット
    `2cef9f8` の履歴に残る）
  - `.claude/settings.json` に `PostToolUse`(Write|Edit) フックを追加。
    `.go`/`go.mod`/`go.sum`編集時のみ`make build`を実行（jqでfile_pathを
    判定）、それ以外はスキップ。実際にEditで発火することを確認済み
  - `engine/doc.go`・`cmd/execdb/main.go` はまだ骨格のみ（ロジック未実装）
- Step 2で以下を実施済み（2026-09-04時点）:
  - `engine/footer.go`（`Magic`/`FooterSize`/`FormatVersion`定数、`Info`型、
    `Inspect(path)`、フッターのエンコード/デコード。整数はbig-endian）
  - `engine/serialize.go`（`serialize()`/`deserializeInto()`。Step1で確定した
    実シグネチャに基づく実装）
  - `engine/engine.go`（`DB`型、`New`/`Open`/`OpenSelf`、`Exec`/`Query`/
    `QueryRow`/`Info`/`Close`。**`Exec`/`Query`/`QueryRow`は`db.keeper`
    経由に統一**——理由は下記「Step 2で確定した事実」参照）
  - `engine/persist.go`（`Snapshot`/`SnapshotTo`/`Overwrite`/`Load`。
    削除済みPoCの退避rename方式・`.execdb_old`掃除ロジックを移植し、
    tmp+renameによるアトミック書き込み、`go run`一時バイナリに対する
    `Overwrite`の明示エラー化を追加）
  - テスト一式（`footer_test.go`/`engine_test.go`/`serialize_test.go`/
    `persist_test.go`）を作成、全PASS。`persist_test.go`の
    `TestOverwriteEndToEnd`は`engine/testdata/overwritehelper`を実際に
    `go build`し、2回実行（seed→read）することで`.overwrite`の実機相当の
    動作確認まで実施済み
  - `make check`・`make build`（`cmd/execdb`込み）・`net`直接依存なしを確認済み
  - 仕様書§6（`Snapshot(path)`確定、`OpenSelf()`追加）・§4（`.load`のバージョン
    警告は呼び出し側責務）・naming.md（対応表更新）へ反映済み
- Step 3で以下を実施済み（2026-09-04時点）:
  - `cmd/execdb/filename.go`（naming.mdのファイル名生成ルールを1関数
    `snapshotFilename`に集約、`-o`/`-t`/`.snapshot`が共用）＋
    `filename_test.go`（全パターンPASS）
  - `cmd/execdb/format.go`（クエリ結果の出力、sqlite3の`list`モード相当
    ＝`|`区切り・ヘッダなし・NULLは空文字列）
  - `cmd/execdb/repl.go`（REPLループ、`.help`/`.exit`/`.quit`/`.tables`/
    `.schema`/`.snapshot`/`.overwrite`/`.load`。`bufio.Scanner`のみ、
    TTY判定は`os.ModeCharDevice`、非TTY時はプロンプト非表示）
  - `cmd/execdb/main.go`（`flag`による起動オプション解析——長短フラグを
    同一変数にバインド、`-h`/`--help`は`flag`パッケージの標準動作に委譲、
    §10のバナー、`engine.OpenSelf()`、`-n`サーバーモードでの
    SIGTERM/SIGINT自動保存）
  - **`-p`/`--pg-addr`・`-s`/`--socket`はパースするが、指定時は
    「未実装（Step 4で対応予定）」エラーで即終了する暫定実装**
    （pgwire.go自体はStep 4で新設するため。banner内の"Listening on..."行も
    Step 4まで表示しない）
  - `-u`/`--user`・`-i`/`--snapshot-interval`は計画通り未実装
    （`--help`にも出さない）
  - **実機確認（`make build`成果物を一時ディレクトリへコピーして実施）**:
    データなしバナー、`--help`/`-h`、パイプ経由のCREATE/INSERT/SELECT/
    `.tables`/`.schema`、`.snapshot`で生成した実行ファイルの単体起動、
    `.load`によるデータ取り込み、**`.overwrite`**（別コピーに対して実行し、
    ファイルサイズ増加・`.execdb_old`の即時削除・再起動後のデータ残存・
    バナーの"Loaded snapshot:"表示まで確認）、`-q`（バナー抑制）、
    `-p`/`-s`未実装ガード、`-n`サーバーモード＋SIGTERM自動保存
    （`-o`で指定したファイル名で保存されることも確認）——**すべて成功**
  - `make check`・`go vet`とも問題なし
- Step 4で以下を実施済み（2026-09-04時点）:
  - `cmd/execdb/access.go`（`splitStatements`——文字列リテラル・識別子・
    行/ブロックコメントを考慮した`;`分割、`firstKeyword`、
    `checkExternalAccess`——DDL＋ATTACH/DETACH/PRAGMA/VACUUM/REINDEXを
    拒否、複文の1つでも拒否対象なら全体を拒否）＋`access_test.go`
  - `cmd/execdb/pgproto.go`（メッセージのエンコード/デコード。前置リクエスト
    ヘッダ読み取り、`StartupMessage`パラメータ解析、フロントエンドメッセージ
    読み取り、`AuthenticationOk`/`ParameterStatus`/`BackendKeyData`/
    `ReadyForQuery`/`RowDescription`/`DataRow`/`CommandComplete`/
    `ErrorResponse`の送信。全列OID 25(text)/format 0固定）＋
    `pgproto_test.go`（バイト列フィクスチャ）
  - `cmd/execdb/pgwire.go`（`startPgwire`——TCP/UDS両対応、UDSは起動時に
    stale socket除去・0600権限・終了時削除。`performHandshake`——
    SSLRequest/GSSENCRequestへの`'N'`応答をループ処理、CancelRequestは
    読み捨てて切断。Simple Queryの複文を`splitStatements`で1文ずつ実行し、
    1文ごとに完全なレスポンスサイクルを送る——real PostgreSQLと同じ挙動）
  - `main.go`のバナー・`run()`を更新: `-p`/`-s`の「未実装」ガードを実際の
    `startPgwire`呼び出しに置き換え、"Listening on..."行を追加
  - **実機確認（すべて実際の`psql`で実施、成功）**: TCP経由`SELECT 1`、
    GSSENCRequest→SSLRequestの2段階ハンドシェイク処理、`CREATE TABLE`/
    `ATTACH`/`PRAGMA`が42501で拒否、`SELECT 1; DROP TABLE t`の複文
    バイパスが拒否、`BEGIN`/`COMMIT`のコマンドタグ、**REPLでCREATE/INSERT
    したデータをpsql側のSELECTで読めて、psql側のINSERTをREPL側のSELECTで
    読める**（REPL・pgwireが同一DBを共有するアーキテクチャの実証）、UNIX
    Domain Socket（libpqの`.s.PGSQL.<port>`命名規則で接続確認）、TCP+UDS
    同時待受、`kill -9`後の再起動でのstale socket自動除去
  - `make check`・`go vet`とも問題なし
  - pgwire.mdへ実装知見を追記: `psql`のGSSENCRequest→SSLRequest送信順序、
    UDSのlibpq命名規則制約、複数クライアント同時トランザクションの真の
    分離は未対応（`engine.DB`が単一keeperコネクション経由のため）という
    既知の制約
- Step 5で以下を実施済み（2026-09-04時点）:
  - `tests/pgclient/main.go`（pgx v5。`SELECT 1`/`SELECT 'hello'`/
    `SELECT NULL`の読み取りと、DDL拒否時に`*pgconn.PgError`として
    SQLSTATE 42501が正しく届くことを検証。tests専用の依存として
    `go.mod`に追加、`cmd/execdb`の依存グラフには含まれないことを
    `go list -deps ./cmd/execdb | grep jackc`で確認済み）
  - `tests/e2e.sh`（`make test`の実体）: REPLのCRUD、`.snapshot`→
    単体起動、`.load`、`.overwrite`（コピーに対して実行）、pgwire TCP
    （`psql`のSELECT・DDL/ATTACH拒否・複文バイパス拒否）、
    `tests/pgclient`実行、pgwire UDS、`go install`後のフッター/
    `.overwrite`動作まで、すべて自動化・全PASS
  - **e2e.sh実装中に発覚した重要な事実（`.claude/rules/pgwire.md`・
    `testing.md`へ追記済み）**:
    - `pgx`はデフォルトでExtended Queryプロトコルを使うため、接続文字列に
      `default_query_exec_mode=simple_protocol`を付与しないとExecDBに
      接続できない（`psql`はデフォルトでSimple Queryなので気づかない）
    - 全列OID 25(text)固定の暫定実装は、`pgx`の厳格な型チェックにより
      「数値列でも`*string`でScanする必要がある」という形で実際に制約と
      して顕在化する（`psql`では気づかない）
    - bashの`wait`組み込みは「コマンド置換のサブシェル経由で得たPID」には
      使えない（`kill -0`ポーリングに置き換えて対処）。サーバーモード
      プロセスは作業ディレクトリ相対で自動保存するため、バックグラウンド
      起動時は`(cd "$WORK" && exec ...)`で明示的に切り替える必要がある
  - `golang.org/x/text`にHIGH深刻度のCVE（CVE-2026-56852）が`trivy`で
    検出され、v0.41.0へアップグレードして解消（pgx経由の間接依存）
  - `.github/workflows/test.yml`: ubuntu/macos/windows × Go 1.26で
    `make check`（Windowsは`choco install make`を前段で実行）、および
    別ジョブで`trivy`（vuln+license、HIGH/CRITICALで失敗）を追加
  - `make check`・`make test`とも問題なし、`trivy`もクリーン
- **初回push後にCIで2件のエラーが発覚、修正済み（2026-09-04）**:
  - **Windows: `make check`の`fmt-check`が失敗。** 原因は
    `gofmt -s -l . | tee /dev/stderr`が Windows（Git Bash/MSYS）環境では
    `tee: /dev/stderr: No such file or directory`で落ちること。
    `Makefile`の`fmt-check`を`tee /dev/stderr`に依存しない実装
    （変数に受けて`[ -n ... ]`で判定、`echo ... 1>&2`で出力）に変更。
    合わせて、**Windowsランナーのgit checkoutがCRLFに変換すると
    `gofmt`が全ファイルを「未整形」と誤検知する**既知の問題を先回りして
    防ぐため、`.gitattributes`（`* text=auto eol=lf`）を追加した
    （まだ発生していなかったが、`fmt-check`修正だけでは踏む可能性が
    高かったため合わせて対処）。
  - **trivyジョブ: `Unable to resolve action
    aquasecurity/trivy-action@0.36.0`。** タグ名の`v`プレフィックスが
    抜けていたのが原因（正しくは`v0.36.0`）。修正済み。
  - 教訓: GitHub Actionsのaction参照は`v`プレフィックスの有無を
    `git ls-remote`やGitHub APIで事前に確認すること。また、シェル
    スクリプト・Makefileで`/dev/stderr`等のUnix固有パスに依存する記述は
    Windowsランナーでの実行を想定して避けること。
  - 2回目のpush後、**Windows限定でもう1件発覚・修正済み**:
    `engine/persist_test.go`の`TestSnapshotPreservesEnginePrefix`が
    Windowsで失敗（`expected an engine-carrying Snapshot to be
    executable, got mode -rw-rw-rw-`）。**Windowsには`chmod`による
    実行ビットという概念が無く、実行可能性は拡張子（`.exe`等）でのみ
    判定される**ため、`os.Chmod(0o755)`を呼んでも`Stat().Mode()`の
    パーミッションビットには反映されない。テストの当該アサーションを
    `runtime.GOOS != "windows"`でガードして対処（拡張子付与は
    `cmd/execdb`側の責務であり`engine`の`Snapshot`が制御すべき事柄
    ではない、というnaming.mdの役割分担とも整合）。`GOOS=windows`への
    クロスビルド・`go vet`は事前にローカルで確認できることを確認済み
    （実行結果の差異はクロスビルドでは検出できないため、この種の
    platform依存アサーションの誤りは実際にCIで走らせないと発覚し
    なかった）。
  - **3回目のpush後、ubuntu/macos/windowsの`make check`・`trivy`
    すべてgreenになったことを確認済み（2026-09-04）。**
- **フェーズ①完了の判定（PLAN.md記載の基準、すべて満たした）**:
  `make check`・`make test`が通る／`psql`と`tests/pgclient`（pgx）の
  両方から疎通しDDL系が拒否される／`go install`で入れたバイナリでも
  `.snapshot`/`.overwrite`が機能する／GitHub Actions
  3OSマトリクス・trivyジョブとも実際にgreen。**全項目達成、フェーズ①
  完全完了。**
- **フェーズ②（`engine`ライブラリ開発）の計画立案・承認済み（2026-09-04）。**
  詳細は上記「フェーズ②のステップ」節を参照。
- **フェーズ②Step 1（スパイク・意思決定ゲート）完了（2026-09-04）。**
  `engine/session_spike_test.go`で`memdb`/`sharedcache`の両基盤を実測し、
  **`memdb` VFSを採用、`sharedcache`は不採用**と確定。詳細は上記
  「フェーズ②Step 1で確定した事実」節を参照。`make check`（`CGO_ENABLED=0`）
  ・`go test -race`（`CGO_ENABLED=1`）ともに green を確認済み。
- **フェーズ②Step 2（基盤置き換え）完了（2026-09-04）。**
  - `engine/errors.go`（新規）: `ErrClosed`/`ErrNoData`/`ErrTooLarge`/`ErrBusy`
    を追加、`ErrNotOverwritable`を`persist.go`から移設
  - `engine/backup.go`（新規）: `backupper`インターフェース、
    `backupInto(conn, dstDSN)`（オンラインBackup API経由でconnの内容を
    生きているDBへ複製）
  - `engine/serialize.go`: `(db *DB) serialize()`を廃止し、任意のコネクションを
    受け取る自由関数`serializeConn(conn)`に変更。`loadBlobInto(blob, dstDSN)`
    を新設（捨てコネクションへ`Deserialize`→`backupInto`の合成）。
    `Open`/`OpenSelf`/`Load`は全てこれを経由するよう統一
  - `engine/engine.go`: `DB`に`dsn string`・`closed bool`フィールドを追加。
    `newSharedCacheDB`を`newLiveDB`へ改名し、DSNを`memdb` VFS
    （`file:/execdbN?vfs=memdb&_busy_timeout=5000`）へ変更。
    **`Exec`/`Query`/`QueryRow`を`db.keeper`経由から`db.sdb`（コネクション
    プール）経由へ変更**——Step 1でbackup APIによる伝播が確認できたため、
    フェーズ①の「全SQL実行をkeeperに集約」という回避策が不要になった。
    `keeper`は生きているmemdbストアを維持するためだけに保持し、SQLを
    実行しないことを明記。`Close()`を`closed`フラグで冪等化
  - `engine/persist.go`: `image()`/`Overwrite()`が新設の
    `(db *DB) serializeBarrier()`（専用コネクションで`BEGIN IMMEDIATE`→
    `Serialize()`→`ROLLBACK`——他セッションの書き込み中は`busy_timeout`の
    範囲で待ってから`ErrBusy`）を経由するよう変更。空blobは`ErrTooLarge`で
    ガード。**`Load()`を`*sql.DB`/`keeper`の差し替えから、生きているDBへの
    in-place backupへ変更**——旧keeperの`Close()`が実行中の`*sql.Rows`を
    破断させるバグが解消し、`.load`中も既存セッションが生き続けるように
    なった（フェーズ①の申し送り事項を解決）。`Load`成功後に`db.info`を
    更新（`sourcePath`/`engineSize`は仕様§4通り不変のまま、既存バグ修正）
  - `engine/session_spike_test.go`は削除（フェーズ①の
    `serialize_spike_test.go`と同じ前例に倣い、本実装と回帰テストへ
    置き換え）。既存テストを新設計に追従させつつ、新規テスト
    （`TestOpenIsVisibleToPooledConnections`/`TestLoadKeepsExistingSessionsUsable`/
    `TestInfoReflectsLoadedFile`/`TestCloseIsIdempotent`/
    `TestUseAfterCloseReturnsErrClosed`/`TestSnapshotToRoundTrip`/
    `TestOverwriteSelfLeavesOriginalIntactOnFailure`）を追加。全24テストPASS
  - `make check`（`CGO_ENABLED=0`）・`go test -race ./...`（`CGO_ENABLED=1`）・
    `make test`（`tests/e2e.sh`——REPL・`.snapshot`・`.load`・`.overwrite`・
    pgwire TCP/UDS・`go install`まで全チェック）とも green を確認済み。
    依存追加なし（`go.mod`/`go.sum`変更なし、trivy不要）
- **フェーズ②Step 3（`Session`とcontext API）完了（2026-09-04）。**
  - `engine/footer.go`: `Inspect`から`decodeFooter(footer []byte, size int64) (Info, error)`
    を切り出し（`Inspect`と新設`LoadFrom`で共有）。`MaxDataSize`定数を追加
    （`1<<30`＝1GiB。Step 1実測の約960MiB上限を踏まえた安全側の妥当性チェック用）
  - `engine/session.go`（新規）: `Session`型（専有`*sql.Conn`を保持）。
    `Exec`/`ExecContext`/`Query`/`QueryContext`/`QueryRow`/`QueryRowContext`/
    `Close`（冪等）。**`Begin`/`BeginTx`はあえて追加しない**——`BEGIN`は
    Sessionの専有接続にSQL文としてそのまま流せば十分にトランザクションとして
    機能し、`*sql.Tx`を挟むと`database/sql`が関知しない二重管理になるため
  - `engine/engine.go`: `Exec`/`Query`/`QueryRow`を`*Context`版の薄いラッパーに
    整理し、`ExecContext`/`QueryContext`/`QueryRowContext`をexport。
    `(db *DB) Session(ctx) (*Session, error)`を追加
  - `engine/persist.go`: `LoadFrom(r io.Reader) error`を追加（`SnapshotTo`の対）。
    `Load`/`LoadFrom`共通の`applyLoadedData(info, blob)`ヘルパーに統合
  - `engine/doc.go`: パッケージdocを更新（`net/net-http`という既存タイポを
    `net`/`net/http`に修正、`Session`の説明を追加）
  - **重要な追加発見（`.claude/rules/sqlite-quirks.md`へ記録）:** `memdb`は
    明示トランザクション中でも`SHARED`ロックが文をまたいで保持され続けると
    は限らない。「Bで先に`BEGIN`＋ダミー読み取りしておけば以後ブロックされ
    ない」という設計は実測で効かず、`TestSessionTransactionIsolation`は
    「有界に待った後、正しい最終状態が見える」という実際の挙動に即した形へ
    設計し直した
  - `.claude/rules/naming.md`: 対応表に`db.Session(ctx)` / `db.LoadFrom(r)`行を追加
  - 新規テスト（`TestSessionTransactionIsolation`/
    `TestSessionSeesCommittedWritesFromAnotherSession`/
    `TestSessionContextCancel`/`TestSessionCloseIsIdempotent`/
    `TestLoadFromReaderRoundTrip`/`TestLoadFromReaderWithoutDataIsError`/
    `TestDecodeFooterRejectsOversizedDataLength`/
    `TestExecContextRespectsCanceledContext`）を追加、全32テストPASS
  - `make check`（`CGO_ENABLED=0`）・`go test -race ./...`
    （`CGO_ENABLED=1`）・`make test`（e2e、cmd/execdb結線は未変更ながら
    engine内部の大幅変更の影響確認のため実行）とも green を確認済み
- **フェーズ②Step 4（並行性テストと`-race`の常設化）完了（2026-09-04）。**
  - `engine/concurrency_test.go`（新規）: `TestConcurrentSessionsReadWrite`
    （8並行`Session`×20行、最終行数一致を確認）／
    `TestUncommittedWriteIsInvisibleToOtherSession`（Step 1④の本番版、
    4並行リーダーに拡張）／`TestConcurrentWriteConflictIsHandledByBusyTimeout`
    （Step 1⑤の本番版、busy_timeoutで実際に待たされたことを経過時間で検証）／
    `TestSnapshotDuringConcurrentWritesProducesConsistentImage`
    （書き込みループ中に5回`Snapshot`し、各出力を`Open`して
    `PRAGMA integrity_check`が`ok`）／`TestLoadDuringConcurrentReads`
    （読み取りループ中に`Load`しても読み取り側がエラーにならず、
    `Load`後は新データが見えることを確認）。`-race`で10回連続実行し
    flakinessなしを確認済み
  - `Makefile`: `race:`ターゲット追加（`CGO_ENABLED=1 go test -race -count=1
    ./...`）。**`check`には含めない**（`PostToolUse`フック経由の速度を保つ
    ため）。`check-deps`を再定義: ①`engine`が`net`/`net/http`を直接import
    しないこと（現行維持）②`go list -deps ./engine`に`net/http`が現れない
    こと（推移的依存の`net`自体は`modernc.org/libc`→`google/uuid`経由で
    解消不能なため対象外。仕様書§6の文言修正はStep 6で対応）
  - `.github/workflows/test.yml`: `race`ジョブを新設
    （`ubuntu-latest`/`macos-latest`限定。`-race`はcgoを要求し、
    windows-latestには標準でCコンパイラが無いため対象外）
  - `.claude/rules/testing.md`: `-race`の運用方針（`make race`を`check`と
    分離した理由、CIでWindowsを対象外にした理由）を追記
  - `make check`・`make race`・`make test`（e2e）とも green を確認済み
- **フェーズ②Step 5（`cmd/execdb`結線）完了（2026-09-04）。**
  - `cmd/execdb/pgwire.go`: **1 TCP/UDS接続 = 1`engine.Session`**に変更
    （`handleConnection`冒頭で`db.Session(ctx)`、`defer sess.Close()`）。
    接続ごとの`context.Context`を`handleSimpleQuery`/`execOneStatement`に
    貫通させ、`sess.ExecContext`/`sess.QueryContext`で実行。
    `watchForDisconnect`（新規）——クエリ実行中だけ別goroutineで`conn`への
    1バイトRead待ちを行い、クライアント切断（またはプロトコル違反の
    予期しないデータ）を検知して`cancel()`する。`stop()`は
    `SetReadDeadline`で強制的にReadを解除してから確実にgoroutineの終了を
    待ってから戻る（`conn`の二重読み取りレースを避けるための同期）。
    `handleSimpleQuery`に`txState`（`'I'`/`'T'`/`'E'`）追跡を追加:
    `BEGIN`成功→`'T'`／`COMMIT`・`ROLLBACK`・`END`成功→`'I'`／`'T'`中の
    エラー→`'E'`。**`'E'`中は`COMMIT`/`ROLLBACK`/`END`以外をSQLSTATE
    `25P02`で拒否**（表示だけ`'E'`にして実行を通す中途半端な実装は
    しない）
  - `cmd/execdb/pgproto.go`: `sqlstateInFailedTransaction = "25P02"`を追加
  - `cmd/execdb/repl.go`: `runREPL`が起動時に1本`Session`を張り、`execSQL`・
    `.tables`（`cmdTables`）・`.schema`（`cmdSchema`）はそのSession経由に。
    `.snapshot`/`.overwrite`/`.load`はDBレベル操作なので`db`経由のまま
    （必須の変更——`ResetSession`がロールバックしないため、素朴に
    `db.Exec`のままだとREPLの`BEGIN`が壊れる。N-7）
  - `tests/pgclient/main.go`: `checkTransactionIsolation`（2接続で
    BEGIN/INSERT→他方から見えない→COMMIT→見える。`memdb`の直列化特性を
    踏まえgoroutine+boundedな待ちで検証）／`checkFailedTransactionState`
    （tx中にエラー→次の文が25P02→ROLLBACKで復帰）／
    `checkDisconnectDuringQuery`（クエリのcontextをタイムアウトさせて
    pgxに接続を諦めさせ、直後の別接続がすぐ繋がることでサーバーが
    居座っていないことを確認）を追加
  - `tests/e2e.sh`: pgtcpサーバーを空バイナリではなくテーブル入りの
    `snap1`スナップショットから起動するよう変更（DDLは外部I/F経由で
    拒否されるため、pgclientの新規チェックにはテーブルが最初から必要）。
    FIFO経由でREPLへ`.load`を送りつつ、1本のpsqlセッション（heredoc＋
    `\! sleep`で接続を保持したまま）が`.load`前後両方のデータを見られる
    ことを確認する新規チェックを追加。3回連続実行でflakinessなしを確認
  - `.claude/rules/pgwire.md`:「既知の制約: トランザクションの真の並行
    分離は未対応」節を削除し、解決済みの記述（`Session`による分離、
    `'I'/'T'/'E'`、切断時キャンセルの範囲と`CancelRequest`との違い）に
    差し替え
  - `make check`・`make race`・`make test`（e2e、3回連続実行）とも
    green を確認済み
- **フェーズ②Step 6（仕様書・ルール・PLANへの反映）完了（2026-09-04）。**
  - `execdb_spec.md`: §2（`Session`による同時実行制御の実現手段、
    `sync.RWMutex`はメタデータ保護でありSQLの同時実行制御ではない旨を明記）／
    §4（`.load`のバージョン警告はフッターのフォーマットバージョンの比較で
    あると訂正、既存セッションが`.load`後も維持されること、データ無し
    ファイルへの`.load`はエラーでメモリ状態不変であることを追記）／
    §6（`net`の推移的依存に関する記述を訂正、責務分担表に`Session`/
    `io.Reader`を追加、`Session`のAPIサブセクションを新設）／§7（採用DSNを
    `memdb` VFSへ更新、`Deserialize`が伝播しない理由とBackup APIによる解決を
    明記、`.snapshot`のアトミック書き込みと並行書き込み下の一貫性保証を追記、
    `.overwrite`の失敗時ロールバックと`go run`一時バイナリ拒否を追記、
    サイズ上限を「2GB未満」から実測値（約1GiB、`memdb`採用時）に訂正）／
    §8（`ReadyForQuery`の`'I'/'T'/'E'`と`25P02`の扱いを明記）を反映。
    削除済みの`serialize_spike_test.go`への言及も現状に合わせて修正
  - `.claude/rules/pgwire.md`: 型マッピング節に`ColumnTypes()`実測結果
    （`Next()`前でも正しい値を返す）をフェーズ④への申し送りとして追記
  - `.claude/rules/naming.md`: `Session`行の説明を「保持する想定」から
    実装済みの記述に更新
  - `PLAN.md`: 「フェーズ③・④への申し送り」節を新設（`.dump`/`.import`/
    スキーマ内省API/`--snapshot-interval`/REPLのCtrl+C はフェーズ③、
    型マッピング/`--user`認証/`CancelRequest`/Extended Queryはフェーズ④、
    という形で整理）
  - **副産物として、`TestSnapshotDuringConcurrentWritesProducesConsistentImage`
    （フェーズ②Step 4で追加）のflakinessを発見・修正。** 書き込みループが
    文と文の間で一切yieldしない設計だったため、バリアの`BEGIN IMMEDIATE`が
    `busy_timeout`（5秒）いっぱい飢餓状態になり`ErrBusy`で失敗することが
    実測で約6割の頻度で発生した。書き込みループに1msの`time.Sleep`を追加
    （現実のワークロードにより近づける）、かつ`Snapshot`呼び出し自体を
    `ErrBusy`に対して有界リトライする形に修正（`ErrBusy`は設計上正当な
    リトライ可能条件であり、初回発生を即失敗扱いにすべきではないため）。
    修正後、単体8回連続・`-race`3回連続でflakinessなしを確認
  - `make check`・`make race`・`make test`（e2e）とも green を確認済み
- **フェーズ②（`engine`ライブラリ開発）完了。** 全6ステップ（スパイク→
  基盤置き換え→Session/context API→並行性テスト→`cmd/execdb`結線→
  ドキュメント反映）を完了し、`engine`は複数の独立したクライアントを
  安全に同時に扱えるライブラリとして作り直された。次のアクションは
  フェーズ③（REPL開発）の計画立案。

## フェーズ③のステップ

REPL開発フェーズは、以下のステップで進める（2026-09-04、計画レビュー済み）。ゴールは
「仕様書§3のREPLコマンド体系を宣言どおり完成させ、REPLをフェーズ①の薄い動作確認用
ループから、sqlite3 CLIに準じた実用的な対話環境に引き上げること」。`.headers`/`.mode`/
`.import`/`.dump`/`--snapshot-interval`が未実装のまま残っており、文の区切り判定
（`repl.go`の`strings.HasSuffix(trimmed, ";")`）にも既知のバグ（文字列リテラル内の`;`で
誤分割）がある。

**この計画で確定した方針:**

| 論点 | 決定 |
| :--- | :--- |
| `.mode`の範囲 | list / column / csv / json / line の5つ。box/markdown/html等の装飾系は不採用（`.claude/rules/cli-output.md`） |
| REPLのCtrl+C | sqlite3準拠。クエリ実行中は中断のみ、アイドル時は1回目で終了しない、連続2回目で終了。非対話（パイプ）時はハンドラ登録せず従来どおり即終了 |
| `.import`のテーブル未存在時 | sqlite3準拠で自動CREATE（全TEXT列）。フィールド数不一致は行番号付きエラーで中断・ロールバック（sqlite3との意図的な相違） |
| スキーマ内省API | `engine`には切り出さない。`.tables`/`.schema`/`.dump`は`cmd/execdb`側の生SQLのまま |

**ステップ構成:**

1. **Step 1: REPL基盤の再構築** — `access.go`から`scanStatements`（remainder付き）を
   切り出し文の区切りバグを直す、`looksLikeRowReturning`を`firstKeyword`ベースに、
   REPL状態を`repl`構造体へ集約、`dotcmd.go`でsqlite3準拠のクォート付き引数パース、
   `.exit [CODE]`。
2. **Step 2: 出力フォーマット** — `.mode`（list/column/csv/json/line）・`.headers`。
   `format.go`を全面書き換え。
3. **Step 3: `.dump`** — スキーマ＋データをSQL文として出力。リテラル化はSQLite自身の
   `quote()`に委譲（自前エスケープ実装はしない）。
4. **Step 4: REPLの複合文対応（`sqlite3_complete`統合）** — Step 3で発覚した
   「REPLの文スキャナがCREATE TRIGGERのBEGIN...ENDを理解できない」問題への対応
   （当初計画外、Step 3完了後にユーザー指示で追加）。ナイーブなBEGIN/END深さカウントは
   CASE式のENDと衝突するリスクがあるため不採用とし、本家`sqlite3`と同じ
   `sqlite3_complete()`（`modernc.org/sqlite`経由、新規外部依存ではない）を
   `engine.Complete(sql string) (bool, error)`として追加、REPL側は
   `scanStatements`の候補境界をこれで検証し直す。
5. **Step 5: `.import`** — CSV読み込み、テーブル未存在時は自動CREATE、SAVEPOINTで
   一括投入。**`engine.Session`に`Prepare`/`PrepareContext`を追加**。
6. **Step 6: `--snapshot-interval`とCtrl+C** — `-i`のティッカーgoroutine、保存経路
   （`.snapshot`/自動保存/`-i`）の共通ヘルパー化。Ctrl+Cはsqlite3の
   `interrupt_handler`と同じ状態機械（クエリ中断／アイドル1回目は継続／連続2回目で終了）。
   最大リスクのステップのため最後に配置。
7. **Step 7: 仕様書・ルール・PLANへの反映** — §3/§6/§9/§10の反映、cli-output.md
   （出力モード・SIGINTの扱い）・naming.mdへの追記。

**フェーズ③完了の判定:** `make check`/`make race`/`make test`が通る／`.mode`5種×
`.headers`がsqlite3同等の書式／`.dump`の出力を空DBへ流し込むとラウンドトリップする／
トリガーを含むSQLをREPLへ直接複数行入力しても正しく1文として実行される／`.import`が
自動CREATE・不一致時ロールバックを含めて動く／`-i`で定期スナップショットが生成される／
実端末でCtrl+Cの状態機械（中断・継続・連続2回終了）が確認できる／GitHub Actions
3OSマトリクス＋raceジョブ＋trivyがgreen／仕様書と`.claude/rules/`が実装と一致している。

計画の全文は `/home/vscode/.claude/plans/step-step-step-crispy-graham.md` を参照
（セッションログにも詳細が残る）。

## フェーズ④のステップ

PostgreSQL互換ワイヤープロトコル開発フェーズは、以下のステップで進める
（2026-09-04、計画レビュー済み）。ゴールは「主要なPostgreSQLドライバが
デフォルト設定のまま、正しい型で、必要なら認証付きで接続・利用できる状態に
外部I/Fを完成させること」。

**計画時の調査で判明した、フェーズ③申し送りに無かった重要事実:**

| # | 事実 |
| :-- | :--- |
| P4-1 | Simple Queryのみの実装では主要ドライバがデフォルト設定で接続できない。`pgx`は`default_query_exec_mode=simple_protocol`、pgJDBCは`preferQueryMode=simple`の明示指定が必要、**Npgsqlは常にExtended Queryのため接続不可**——仕様書§8の狙いと衝突 |
| P4-2 | pgJDBCは接続直後に`SET extra_float_digits = 3`等を送る。SQLiteは`SET`を解釈できず、現状は素通しして構文エラーになり接続が確立しない（`checkExternalAccess`の拒否対象にも入っていない） |
| P4-3 | devcontainerには`python3`と`psql`しか無い（`pip`/`java`/`node`/`dotnet`は未導入） |

**この計画で確定した方針:**

| 論点 | 決定 |
| :--- | :--- |
| Extended Query プロトコル | 実装する（Step 5着手前に仕様書§8の採用範囲表を先行更新） |
| 他言語ドライバ検証の範囲 | Python（psycopg）・Java（pgJDBC）・Node.js（node-postgres）。.NET（Npgsql）は見送り、Extended Query実装により接続可能になっている前提だけ整えて申し送る |
| 型マッピングの積極性 | Step 1のスパイクで`ColumnTypes()`を実測してから確定（アフィニティ違反値の実際の挙動を見てから判断） |
| 他言語ランタイムの導入 | 都度インストールし保留事項へ記録、フェーズ④完了時（Step 8）にdevcontainer.jsonへ統合 |

**ステップ構成:**

1. **Step 1: スパイク【意思決定ゲート】** — `ColumnTypes()`の網羅実測（宣言型別・
   式/集約・アフィニティ違反値）で型マッピング表を確定。各ドライバ（psql/psycopg/
   pgJDBC/node-postgres）のメッセージ列を記録用サーバーで実測しStep 3/5のスコープを
   確定。SQLiteが`$1`プレースホルダを解釈できるか、prepared statementから実行前に
   列情報を取れるかを検証。
2. **Step 2: 型マッピング** — `RowDescription`のOID化、`bytea`/`float8`等の
   Postgresテキスト形式エンコード（`pgtype.go`新設）。
3. **Step 3: `SET`/`SHOW`互換シム** — pgwire層で横取りしSQLiteへ渡さない
   （`pgsession.go`新設）。
4. **Step 4: `-u`/`--user`認証** — cleartext password。`golang.org/x/term`を
   新規依存として追加（要確認）。
5. **Step 5: Extended Query プロトコル** — Parse/Bind/Describe/Execute/Sync/Close/
   Flush。エラー時はSyncまで読み捨てる同期規則、`watchForDisconnect`の前提見直しが
   最大の難所。
6. **Step 6: `CancelRequest`/`BackendKeyData`** — 接続ごとのPID/secretレジストリ。
7. **Step 7: 他言語ドライバ検証＋CI** — Python/Java/Nodeを都度導入、`tests/drivers/`、
   CIに`drivers`ジョブ新設（ubuntu限定）。
8. **Step 8: 仕様書・ルール・PLAN・devcontainer統合** — フェーズ④・全実装の締め。
   `PLAN.md`の「devcontainer.json反映待ちリスト」をここで消化する。

**フェーズ④完了の判定:** `make check`/`make race`/`make test`が通る／`pgx`・pgJDBC・
node-postgres・psycopgがデフォルト設定のまま接続しSELECT/DML/トランザクションが
動く／数値・BLOB列が各ドライバの型付きAPIで読める／DDLが全ドライバから42501で
拒否される／`-u`認証（正誤パスワード・`EXECDB_PASSWORD`優先・`--no-repl`未設定時の
起動中止）が動く／別接続からの`CancelRequest`で実行中クエリが中断される／
GitHub Actions（3OS＋race＋trivy＋drivers）がgreen／仕様書と`.claude/rules/`が
実装と一致／devcontainer.jsonの反映待ちリストが空になる。

計画の全文は `/home/vscode/.claude/plans/step-step-step-crispy-graham.md` を参照。

## フェーズ④Step 1で確定した事実（型マッピング・プレースホルダ・Describe、2026-09-04実測）

`/tmp`の使い捨てGoプログラム（コミットせず。`modernc.org/sqlite`直結、`engine`は
経由しない素の`database/sql`実験）で実測。

### (a) `ColumnTypes()`の網羅実測 → 型マッピング方針を確定

- **`DatabaseTypeName()`（宣言型）はSQLiteの`decltype`文字列をそのまま返す
  （`VARCHAR(10)`/`DECIMAL(10,2)`/`BOOLEAN`/`DATETIME`のようにSQLite標準5分類へ
  正規化されない）。** OID決定には、SQLiteの型アフィニティ判定アルゴリズム
  （`decltype`に`INT`/`CHAR|CLOB|TEXT`/`BLOB`/`REAL|FLOA|DOUB`のいずれの部分文字列も
  含まれなければNUMERIC扱い、という公式ルール）をこちらで実装する必要がある。
- **式・集約・リテラル列（`decltype`が空文字列）でも、`ColumnTypes()`を`Next()`前に
  呼ぶと`ScanType()`が実際の値の型を返す。** `1+1`→`int64`、`'x'`→`string`、
  `x'00ff'`→`[]uint8`、`CASE...END`→`string`、`NULL`単体リテラル→`nil`
  （フェーズ②で確認済みの「`Next()`前でも正しい値を返す」の追加実測——**内部で
  最初の1行を先読みしている**ことが今回判明。空の結果セット（`WHERE 0`等）では
  `ScanType()`は`nil`に戻る）。
- **重大な罠: `ScanType()`が示す型と、実際に`interface{}`へ`Scan`した際に返る
  Goの動的型は一致しないことがある。** `BOOLEAN`宣言列は`ScanType()`が`bool`を
  返すが、`interface{}`へ`Scan`すると実際には`int64(1)`が返る（SQLiteは真の
  bool型を持たず整数格納のため）。**OIDを決めるための型情報と、実際の値を
  Postgresテキスト形式へエンコードする処理は別々に設計する必要がある**
  （前者は`DatabaseTypeName()`ベースのアフィニティ判定、後者は`Scan`後の
  実際のGo動的型に対する型スイッチ、の二段構え）。
- **型アフィニティに反する値（`INTEGER`列に`'hello'`を`INSERT`等）は、
  `DatabaseTypeName()`は宣言どおり`"INTEGER"`のままだが、`Scan`結果は
  `string`になる。** 積極的にOIDをマップする以上、この不一致は原理的に
  残る既知の制限として扱う（宣言型に反するデータを意図的に入れた場合のみ
  発生し、正規のワークロードでは稀という判断）。
- **`DATETIME`宣言列は、文字列を`INSERT`しても`Scan`結果が`time.Time`になる**
  （`modernc.org/sqlite`が`decltype`に`DATE`/`TIME`を含む列を検出して自動変換
  している）。OIDマッピングは`BOOLEAN`・`DATE`/`DATETIME`/`TIMESTAMP`を
  アフィニティ判定より優先する特別扱いとする。
- ビュー・サブクエリ経由の列は、元の宣言型（`INTEGER`/`TEXT`等）をそのまま
  引き継ぐ（アフィニティ判定がそのまま使える）。

**→ 決定: OID決定は`DatabaseTypeName()`（宣言型）ベースのアフィニティ判定を
第一優先とし、`BOOLEAN`/`DATE`系は特別扱い、宣言型が空（式・集約・リテラル列）
の場合のみ`ScanType()`（先頭行の実測値）にフォールバックする。空の結果セット
かつ宣言型も無い場合はtext(25)を最終フォールバックとする。値のテキスト
エンコードは常に`Scan`後の実際のGo動的型を型スイッチして行い、OIDとの
不一致（アフィニティ違反・`BOOLEAN`のint64等）はエンコード側で吸収する。**

### (c)-1 `$1`形式のプレースホルダはSQLiteがネイティブに解釈できる

`SELECT ... WHERE a = $1`（`db.Query(query, 2)`）、`$2`/`$1`のような番号の
飛び・逆順混在も含めて**無変換でそのまま動作することを確認**（SQLiteの
`$AAAA`パラメータ構文はAAAA部分に数字も許容するため、たまたまPostgresの
`$N`と表記が一致する）。**→ 決定: Extended QueryのSQL文はプレースホルダの
書き換え層なしでそのまま`Session.PrepareContext`へ渡せる。**

### (c)-2 statement-level Describe（`RowDescription`をBind前に返す）は新規engine APIなしで実現できる

`Prepare`した文に対し、**全プレースホルダへNULLを仮バインドして実行**
（`stmt.Query(nil, nil, ...)`）すると、行が0件でも宣言型ベースの
`ColumnTypes()`は正しく返ることを確認（`WHERE a = $1`にNULLを渡すと
0件になるが、列のdecltypeは`a INTEGER`のまま正しく取得できた）。
一方、**プレースホルダの値そのものに依存する式列（`SELECT $1 + 1`）は
NULL仮バインドだと`ScanType()`も`nil`になり型を特定できない**——これは
Postgres本来のDescribeでも原理的に難しい领域（パラメータ値未確定の時点では
式の結果型を静的に知りようがない）ので、text(25)へのフォールバックで
妥当と判断。

**→ 決定: `engine/describe.go`は不要。** `cmd/execdb`側で
①`looksLikeRowReturning`（既存, `access.go`）で行を返す文か判定
②SQL文中の`$N`/`?`の個数をテキストスキャンで数える（新規の軽量ヘルパー、
SQLiteのC APIやreflectionは使わない）
③行を返す文なら、SAVEPOINT/ROLLBACKで囲んだ上で全プレースホルダにNULLを
バインドして試験実行し、その`ColumnTypes()`から`RowDescription`を構築
④行を返さない文は`ParameterDescription`＋`NoData`のみ返す
という設計で完結する。**SAVEPOINT/ROLLBACKで囲むのは、`INSERT ... RETURNING`
のような「構文上はINSERTだが行を返す」文を万一`looksLikeRowReturning`が
取りこぼした場合の安全網**（副産物として、`looksLikeRowReturning`が
`RETURNING`句を考慮していない既存の抜けを発見——`INSERT/UPDATE/DELETE ...
RETURNING`は本来行を返すが、現在のSimple Query経路（`pgwire.go`の
`execOneStatement`）も含めてExecContextで処理してしまい、RETURNING結果が
握りつぶされる。**Step 2または5で`looksLikeRowReturning`にRETURNING検出を
追加し修正する**——新規の発見だがフェーズ④のスコープ内で自然に直る)。

### (b) ドライバのメッセージ列の実機捕捉は今回省略、Step 5/7の実接続で確認する方針に変更

`psql`以外（psycopg/pgJDBC/node-postgres）は現時点でdevcontainerに未導入。
Extended Queryのメッセージフロー自体はPostgreSQLプロトコル仕様として
標準化・安定しているため、記録用サーバーでの実機捕捉は行わず、Step 5の
実装はプロトコル仕様に基づいて進め、**Step 7の実ドライバ接続で得られる
差異（pgJDBCの`SET`送出等、P4-2で既知）に対して都度対応する**方針とする
（フェーズ③がStep 3→Step 4で実施したのと同じ「実装→実機確認→ギャップ
発覚→追加対応」のサイクルを踏襲）。

## フェーズ④Step 2（型マッピング）完了（2026-09-04）

Step 1のスパイクで確定した方針（宣言型ベースのアフィニティ判定を第一優先、
式・集約列のみ先頭行の実測値へフォールバック）をそのまま実装。

- `cmd/execdb/pgtype.go`（新規）: OID定数（`bool`16/`bytea`17/`int8`20/
  `text`25/`float8`701/`timestamp`1114/`numeric`1700）。`columnOID(*sql.ColumnType)`
  が`DatabaseTypeName()`（宣言型）を`affinityOID`（SQLite公式の型アフィニティ
  判定アルゴリズムを実装、`BOOLEAN`/`DATE`系はアフィニティ判定より優先する
  特別扱い）へ通し、宣言型が空（式・集約・リテラル列）の場合のみ
  `scanTypeOID`（`ScanType()`ベース）にフォールバック。`pgEncodeValue(oid, v)`が
  実際にScanされたGoの動的型を型スイッチしてPostgresテキスト形式へ変換
  （BLOB→`\x`+16進、REAL→`strconv.FormatFloat`＋NaN/Infinity特別扱い、
  `BOOLEAN`列のint64値→`t`/`f`——Step 1で発見した「宣言型はBOOLEANなのに
  Scan結果はint64」という不一致をここで吸収）。`pgTypLen(oid)`で
  RowDescriptionの固定長フィールドを返す
- `cmd/execdb/pgproto.go`: `writeRowDescription`の引数を`[]string`から
  `[]pgColumn{name, oid}`へ変更し、実際のOID・typlenを送るように
  （旧: 全列OID 25 text固定）
- `cmd/execdb/pgwire.go`: `sendRows`が`rows.Columns()`ではなく
  `rows.ColumnTypes()`から`pgColumn`を組み立て、`formatValue`ではなく
  `pgEncodeValue`で値をエンコードするよう変更
- `cmd/execdb/format.go`: `formatValue`のdocコメントを更新
  （「フェーズ4で対応」という古い記述を、pgwireは別エンコーダ
  `pgEncodeValue`を持つという実装済みの説明へ差し替え）
- **副産物: `looksLikeRowReturning`（`access.go`）の既存の抜けを発見・修正。**
  `INSERT/UPDATE/DELETE ... RETURNING`（SQLite 3.35+）は本来行を返すが、
  先頭キーワードだけを見る従来の判定では常に`false`になり、Simple Query
  経由でも`ExecContext`に回されてRETURNING結果が握りつぶされていた。
  `hasReturningClause`（クォート・コメントを考慮したトップレベルの
  `RETURNING`キーワード検出、`matchesWordAt`で単語境界も判定）を追加し、
  `INSERT`/`UPDATE`/`DELETE`は`RETURNING`句の有無で判定するよう修正
- 新規テスト: `pgtype_test.go`（宣言型別・式/集約列・アフィニティ違反値の
  `columnOID`、`affinityOID`の網羅、`pgEncodeValue`のNaN/Infinity/BLOB/
  BOOLEAN込みテーブル駆動テスト）、`pgproto_test.go`の
  `TestWriteRowDescription`をOID/typlenまで検証する形へ拡張、
  `access_test.go`に`RETURNING`句の検出・非検出（文字列/識別子内の
  "returning"に反応しないこと含む）を追加
- `tests/pgclient/main.go`: `*string`固定Scanを`*int64`/`*float64`/`[]byte`
  への直接Scanへ変更（型が実際に機能していることの検証に変更）。
  トランザクション分離・失敗状態・切断系のチェックも`SELECT 1`/
  `count(*)`の戻り値を`int64`で比較する形に追従
- **実機確認（`psql`でサーバーモード起動、`SELECT`で確認）**: INTEGER/REAL列が
  右詰め表示（psqlが数値OIDと認識）、TEXT列は左詰め、BLOB列が`\x00ff`形式、
  BOOLEAN列の実体（int64の0/1）が`t`として正しく表示されることを確認
- `make check`・`make race`・`make test`（e2e、`tests/pgclient`実行含め
  全項目PASS）とも green を確認済み。依存追加なし

## フェーズ④Step 3（`SET`/`SHOW`互換シム）完了（2026-09-04）

`P4-2`（pgJDBCが接続直後に`SET extra_float_digits = 3`を送り、SQLiteが
`SET`を解釈できず接続確立前に壊れる）への対応。

- `cmd/execdb/pgsession.go`（新規）: `sessionParams`（`map[string]string`、
  接続1本＝1goroutineで直列処理されるためロック不要）。
  `handleSessionCommand(w, params, kw, stmt)`が`SET`/`SHOW`をSQLiteへ渡す
  前に横取りする——`access.go`の`checkExternalAccess`が担う「拒否」/
  「SQLiteへ渡す」の2区分とは別の、**ExecDB自身が応答する第3の区分**
  として`pgwire.go`のディスパッチループ内で処理する設計とし、
  `access.go`の分類器はSQLite向け文の判定という役割を保つ。
  - `SET [SESSION|LOCAL] name (TO|=) value` → 値を`sessionParams`へ保存し
    `CommandComplete("SET")`。SESSION/LOCALのスコープ区別はしない
    （ExecDBにサブトランザクションのパラメータスタックが無いため）
  - `SHOW name` → 1行1列で値を返す（未設定なら空文字列、NULLではない）。
    **`SHOW ALL`は未実装**（実装するとしたらStep 7の実ドライバ確認で
    必要と判明してから、というスコープを広げない判断——`ALL`という
    トークン自体は識別子として正規表現にマッチしてしまうため、
    明示的にエラーへ倒すガードを追加）
  - `RESET`・`BEGIN ISOLATION LEVEL ...`等は今回未実装（Step 1(b)の
    実機捕捉を省略したため、Step 7で実ドライバ確認してから要否判断）
- `cmd/execdb/pgwire.go`: `sendStartupResponse`のパラメータ一覧を
  `startupParameterStatus()`として切り出し、`sessionParams`の初期値
  （`newSessionParams`）と共有——起動直後の`SHOW`が`ParameterStatus`で
  既に伝えた値と一致するようにするため。`handleSimpleQuery`が
  `sessionParams`を引き回し、文ごとのディスパッチループで
  `handleSessionCommand`を`execOneStatement`より先に試す
  （`txState == 'E'`中は`SET`/`SHOW`も他の文と同様25P02で拒否——
  実PostgreSQL準拠）
- 新規テスト`pgsession_test.go`（`SET`の各構文バリエーション・
  クォートエスケープ・未知パラメータの`SHOW`・`SHOW ALL`拒否・
  失敗構文のエラー応答）
- **実機確認（`psql`）**: `SET`→`SHOW`が同一接続内で正しく往復すること、
  別接続からは見えない（接続スコープの分離）こと、失敗トランザクション中の
  `SET`が25P02で拒否されることを確認
- `tests/e2e.sh`にpgwire TCP経由の`SET`/`SHOW`往復チェックを追加
- `make check`・`make race`・`make test`（e2e）とも green を確認済み。
  依存追加なし（`regexp`は標準ライブラリ）

## フェーズ④Step 4（`-u`/`--user`認証）完了（2026-09-04）

仕様書§8「認証（オプトイン、Zero-Authがデフォルト）」・§9の`-u`行をそのまま実装。

- **新規依存: `golang.org/x/term`（BSD-3-Clause）を追加。** `trivy fs
  --scanners license,vuln .`でCVE 0件・ライセンス互換を確認済み
  （`modernc.org/libc`もこの機会に`go.mod`上「間接」から「直接」表記へ
  修正された——`engine/complete.go`が既に直接importしていたのに
  indirectのまま残っていた既存のズレを`go mod tidy`が正した、副産物）
- `cmd/execdb/auth.go`（新規）:
  - `resolveAuth(opts)` / `resolveAuthWith(opts, prompt)`——`--user`指定時の
    パスワード決定（①`EXECDB_PASSWORD`環境変数 ②REPLモードなら対話入力
    ③`--no-repl`なら起動中止のエラー）。`prompt`関数を引数として注入する
    設計とし（`interrupt.go`の`exitFunc`と同じDIパターン）、実端末が無い
    テスト環境でも全分岐を検証可能にした
  - `promptPassword()`——`golang.org/x/term`の`ReadPassword`でマスク入力
  - `authenticateConnection(rw, user, password, startupParams)`——pgwire接続
    ごとの実プロトコル認証。`user`が空ならZero-Auth
    （`AuthenticationOk`のみ）、それ以外は
    `AuthenticationCleartextPassword`→`PasswordMessage`待ち→
    `StartupMessage`の`user`パラメータとパスワードの両方を照合、
    不一致はSQLSTATE `28P01`（実PostgreSQLそのままのコード）で拒否
- `cmd/execdb/pgproto.go`: `writeAuthenticationCleartextPassword`と
  `sqlstateInvalidPassword = "28P01"`を追加
- `cmd/execdb/pgwire.go`: `performHandshake`が`StartupMessage`の
  パラメータを返すよう変更（従来は読み捨てていたが、`user`パラメータの
  照合に必要になったため）。`sendStartupResponse`を
  `sendStartupTail`（`ParameterStatus`〜`ReadyForQuery`）へ改名・
  認証応答部分を分離——`handleConnection`が`performHandshake`→
  `authenticateConnection`→`sendStartupTail`の順に呼ぶ構成に。
  `acceptLoop`/`handleConnection`が`user`/`password`を引き回す
- `cmd/execdb/main.go`: `options`に`user`/`password`
  （`password`はCLIフラグではなく`resolveAuth`が解決——プロセス一覧や
  シェル履歴に残らないようにするため）。`-u`/`--user`をパース、
  `--help`に追記。`run()`冒頭で`resolveAuth`を呼ぶ。バナーに
  認証有効時の1行を追加
- 新規テスト`auth_test.go`: `resolveAuthWith`の全分岐
  （環境変数優先・空文字列環境変数の尊重・サーバーモードでのエラー・
  REPLモードでのプロンプト呼び出し・プロンプトのエラー伝播）、
  `authenticateConnection`（Zero-Auth・正しい認証・パスワード不一致・
  `StartupMessage`の`user`不一致・`PasswordMessage`以外のメッセージ型）
- **実機確認（`psql`）**: 正しいユーザー名・パスワードで接続、誤った
  パスワードが拒否、`StartupMessage`の`user`が`--user`と異なる場合も
  拒否、`--no-repl`かつ`EXECDB_PASSWORD`未設定時に起動が即座にエラー
  終了することを確認
- `tests/e2e.sh`に`-u`/`--user`の3チェック
  （正しい認証情報での接続・誤ったパスワードの拒否・
  `StartupMessage`の`user`不一致の拒否）を追加
- `make check`・`make race`・`make test`（e2e）とも green を確認済み

## フェーズ④Step 5（Extended Queryプロトコル）完了（2026-09-04）

フェーズ④最大リスクのステップ。着手前に仕様書§8・`.claude/rules/pgwire.md`の
採用範囲表をExtended Query採用へ先行更新（`.claude/rules/pgwire.md`の
「スコープを広げる場合はまず仕様書を更新」ルールに従う）。

- `cmd/execdb/pgextended.go`（新規）: `Parse`/`Bind`/`Describe`/`Execute`/
  `Close`の各ハンドラ。`preparedStatement`（`sql`/`stmt *sql.Stmt`/
  `numParams`/`rowReturning`）と`portal`（`stmtName`/`args`/
  `resultFormats`）を接続ごとの`extendedQueryConn`（`statements`/
  `portals`のmap、`inError`フラグ）で管理。`""`（無名文/無名ポータル）は
  常に上書き可能、名前付きは既存があればエラー——実PostgreSQL準拠。
  `countPlaceholders`（クォート・コメントを考慮した`$N`最大値スキャン、
  `access.go`の`hasReturningClause`と同じ骨格）でBind/Describeが要求する
  パラメータ数を決定
- **プレースホルダはStep 1(c)-1の実測どおり無変換でSQLiteへそのまま渡せた**
  （書き換え層は作らなかった）
- **`Describe`はStep 1(c)-2の設計どおり実装**: 行を返す文は全プレースホルダに
  NULLを仮バインドした試験実行（`SAVEPOINT execdb_describe`→
  `ROLLBACK TO`→`RELEASE`で囲む）から`ColumnTypes()`を読み取り
  `RowDescription`を構築。行を返さない文は`NoData`。`engine`側への新規API
  追加は不要だった（想定どおり）
- **エラー時は`Sync`まで後続メッセージを読み捨てる**（`extendedQueryConn.inError`、
  `pgwire.go`のディスパッチループで管理）。`txState`（`'I'/'T'/'E'`）は
  Simple Queryと共有の意味論——`Parse`/`Bind`/`Describe`/`Execute`/`Close`の
  いずれかが失敗し、かつその時点で`'T'`（トランザクション中）なら`'E'`へ
  遷移させる、という判定をディスパッチループに一元化（各ハンドラ個別には
  実装しない設計とした）
- **`watchForDisconnect`はExtended Queryには使わない（意図的な既知の制限）。**
  Simple Queryは「クライアントは応答を受け取るまで次のメッセージを送らない」
  という厳密な要求/応答プロトコルだが、Extended Queryはパイプライン化
  （複数メッセージをまとめて送り、応答をまとめて受け取る）が正当な使い方
  であるため、`watchForDisconnect`のバックグラウンド読み取り監視が
  パイプライン中の正当なメッセージを切断と誤認しかねない。**結果、
  Extended Query経由の実行中クエリは、クライアントが（TCP接続を切らずに）
  消えても、次にこのループが`conn`から読み取ろうとするまで検知されない**
  ——Step 7の実ドライバ確認で問題になれば見直す
- `cmd/execdb/pgproto.go`: `ParseComplete`/`BindComplete`/`CloseComplete`/
  `NoData`/`ParameterDescription`の各writerを追加
- `cmd/execdb/pgwire.go`: `sendRows`から`sendDataRows`を切り出し
  （`RowDescription`送信とデータ行送信を分離——`Execute`は`RowDescription`を
  再送しない実PostgreSQL準拠のため）。`handleConnection`のディスパッチ
  ループに`'P'/'B'/'D'/'E'/'H'/'C'/'S'`を追加
- **重大な追加発見（実機確認で判明、当初のスコープ判断を覆す）:**
  `pgx`のデフォルト（Extended Query）接続で`SELECT`した際、`int8`列が
  `invalid length for int8: 1`のようなエラーで**接続できなかった**。
  調べたところ`pgx`は`Bind`の`resultFormatCodes`で`int8`/`float8`/
  `numeric`/`bool`/`bytea`/`timestamp`列に対し**デフォルトでバイナリ
  結果形式を要求**していた——`bool`/`bytea`は**エラーにすらならず
  黙って誤った値**（`bool`が常に`false`、`bytea`がテキストのバイト列を
  そのまま返す）になっており、エラーになるケースより危険だった。
  これはStep 1のスパイクでは検証していなかった観点（Describe/
  プレースホルダ機構は確認したが、実際のpgx接続でのSELECT自体は
  試していなかった）。ユーザーに相談の上、**int8/float8/bool/bytea/
  timestampの5型のみバイナリ結果形式を実装**（NUMERICは対象外——
  SQLiteのNUMERIC親和性は内部的に必ずINTEGER/REALで格納され真の
  任意精度十進数を持たないため、Postgresの複雑なバイナリNUMERIC形式
  （base-10000桁グループ）を実装する代わりに、**`columnOID`がNUMERIC
  親和性の宣言型を実行時のGo動的型（int64/float64）でint8/float8へ
  振り分ける設計に変更**し、`pgx`がそもそもNUMERIC OIDに対してバイナリを
  要求する状況自体を発生させないようにした）
  - `cmd/execdb/pgtype.go`: `pgColumn`に`binary bool`フィールドを追加。
    `binaryCapableOIDs`（int8/float8/bool/bytea/timestampの5つ）。
    `affinityOID`の戻り値を`(oid uint32, matched bool)`に変更し、
    NUMERIC親和性の catch-all は`matched=false`を返すことで`columnOID`が
    `scanTypeOID`（実行時のGo動的型サンプリング）へフォールバックする
    設計に変更。`pgEncodeValue`が`useBinary bool`引数を受け取り、
    `encodeBinary`（int8→8バイトbig-endian、float8→IEEE754 big-endian、
    bool→1バイト、bytea→生バイト列そのまま、timestamp→Postgresエポック
    （2000-01-01）からのマイクロ秒int64）で実際のバイナリ値を生成。
    型不一致（アフィニティ違反）時はテキストへフォールバックし、
    不正なバイナリペイロードを送らないようにガード
  - `cmd/execdb/pgproto.go`: `writeRowDescription`が列ごとに実際の
    フォーマットコード（binary=1/text=0）を送るよう変更
  - `cmd/execdb/pgextended.go`: `buildResultColumns(cts, resultFormats)`
    ヘルパーを追加——`Bind`が受け取った`resultFormatCodes`
    （`portal.resultFormats`に保持）と`binaryCapableOIDs`の両方を
    満たす列だけバイナリにする。`Describe`（statement）は`Bind`前なので
    常にテキスト（`nil`を渡す）、`Describe`（portal）と`Execute`は
    `portal.resultFormats`を使う
  - 仕様書§8・`.claude/rules/pgwire.md`の採用範囲表・経緯説明を更新
- 新規テスト:
  - `pgextended_test.go`（新規）: メッセージ解析（`parseParseMessage`/
    `parseBindMessage`のバイト列往復・不正形式）、`countPlaceholders`/
    `formatCodeFor`の単体テスト、**`net.Pipe`越しに`handleConnection`を
    実際に駆動するEnd-to-Endテスト**8本（フルラウンドトリップ、
    statement/portal Describeの`ParameterDescription`/`RowDescription`/
    `NoData`、名前付き文の使い回し、エラー→Sync読み捨て、`BEGIN`中の
    エラーでの`'E'`遷移と`25P02`、`Close`後の再利用エラー、バイナリ
    パラメータの拒否）
  - **`net.Pipe`は完全同期・無バッファのため、複数メッセージを応答を
    読まずに送ると自己デッドロックする**（送信側のWriteが受信側のReadを
    待ち、受信側（サーバー）も自分の応答Writeが読まれるのを待つ、という
    循環待機）。**送信をゴルーチン化する`sendPipelined`ヘルパーで回避**
    ——これは同時に本物のExtended Queryパイプライン化の動作確認にもなる。
    新しい知見として今後同種のテストを書く際の注意点
  - `pgtype_test.go`: `affinityOID`の2値返却・NUMERIC catch-allの
    `matched=false`確認、`pgEncodeValue`のテキスト/バイナリ両方の
    テーブル駆動テスト、バイナリ⇔実際の値の型不一致時のテキスト
    フォールバック確認
- `tests/pgclient/main.go`は変更なし（Step 2の型付きScan検証がそのまま
  Extended Queryパスの検証にも使える設計だった）
- `tests/e2e.sh`: `tests/pgclient`を**`pgx`のデフォルト設定
  （Extended Query、`default_query_exec_mode`指定なし）で実行**する
  チェックを追加——これがStep 5の本来の目標（デフォルト設定接続）の
  直接的な証明。Simple Query強制版は別の使い捨てスナップショット・
  サーバー（`pgtcp2`、ポート15537）で独立に実行するよう分離
  （同じテーブルに対し`checkTransactionIsolation`の固定値INSERTを
  2回走らせると累積して失敗するため）
- **副産物: e2e.shの手動デバッグ中に、前回の実行が残したオーバーフローの
  孤児プロセス（ポート15532を掴んだまま）により、新しい`pgtcp`起動が
  サイレントに失敗し、`wait_for_tcp`が古い孤児に接続してしまうという
  問題に遭遇。** `start_server`はバックグラウンド起動の成否を確認せず、
  `wait_for_tcp`も「誰か」がポートを掴んでいれば成功してしまうため、
  孤児プロセスが残っていると気づかずに古いデータへテストしてしまう
  （本セッションでの手動デバッグ由来の孤児であり、`tests/e2e.sh`自体の
  既存の孤児処理——`SERVER_PIDS`配列＋`trap cleanup EXIT`——は正常に機能
  している。ただし人手でバックグラウンドサーバーを都度起動して調査する際は、
  作業後に確実に`kill`するか、`ps aux | grep execdb`で残存確認する習慣が
  必要という教訓）
- `make check`・`make race`・`make test`（e2e、2回連続実行でflakinessなし）
  とも green を確認済み。依存追加なし

## フェーズ④Step 6（`CancelRequest`/`BackendKeyData`）完了（2026-09-04）

- `cmd/execdb/pgcancel.go`（新規）: `cancelKey{pid, secret}`（実PostgreSQLの
  secretと同様、強固な認証ではなく他クライアントによる偶発的な誤爆を防ぐ
  程度の目的なので`math/rand`で十分と判断）をキーに、プロセス全体で1つの
  `globalCancelRegistry`（mutex保護map）が各接続を追跡。`handleCancelRequest`
  が`CancelRequest`の生body（PID＋secret、各Int32）を読み取り、
  `globalCancelRegistry.cancel(key)`で対象接続の現在実行中のクエリを
  キャンセルする（見つからない・secret不一致・アイドル中はいずれも無音の
  no-op——実PostgreSQL準拠）
- **実装中に発見・修正した重大な設計バグ**: 当初は接続の生存期間全体で
  固定の`context.CancelFunc`を1つだけ使い回す設計にしたところ、
  `context.CancelFunc`は一度きりしか呼べない性質のため、
  **`CancelRequest`（あるいは`watchForDisconnect`）が一度でも発火すると、
  その接続の以後すべてのクエリが`context canceled`で永久に失敗する**
  ことが実機確認（`tests/pgclient`）で発覚した（`SELECT 1 after
  CancelRequest: ... context canceled`）。`CancelRequest`は「そのクエリだけを
  中断し接続自体は使い続けられる」という実PostgreSQLの重要な性質を持つため、
  このバグは`CancelRequest`実装で初めて表面化した（`watchForDisconnect`
  単体では「発火後は接続を捨てる」運用だったため、同じ設計上の欠陥が
  フェーズ②から気づかれずに残っていたことも判明）。
  - **修正**: `pgcancel.go`に`connCancel`（mutex保護の`context.CancelFunc`
    フィールド、`begin(cancel) (end func())`/`cancelCurrent()`）を追加——
    `interrupt.go`の`replInterrupts.begin`（フェーズ③Step 6、REPLの
    Ctrl+C用に同じ問題を解決済みだったパターン）とまったく同型。
    `globalCancelRegistry`は`cancelKey`→`*connCancel`で管理し、
    `handleConnection`のメインループが**クエリ（メッセージ）ごとに
    新しい`context.WithCancel`を作って`cc.begin`/`end`で都度登録・解除**
    するよう変更（`db.Session(ctx)`はもう接続生存期間のctxを必要としない
    ——`engine.DB.Session`の実装を確認したところ`sdb.Conn(ctx)`の一回の
    呼び出しにしかctxを使わないため、`context.Background()`で十分と判明）。
    **`for`ループ内では`defer end()`ではなく明示呼び出しにする**——ループの
    中でdeferすると、そのループを含む関数（`handleConnection`）全体が
    終わるまで実行されず、次のクエリが始まる前に確実に解除されないため
    （`stop()`が既にこの明示呼び出しパターンだったのと同じ理由）
- `cmd/execdb/pgwire.go`: `sendStartupTail`が`cancelKey`を受け取り実際の
  値を送るよう変更（従来の`0, 0`固定を解消）。`performHandshake`の
  `CancelRequest`分岐が読み捨てから`handleCancelRequest`呼び出しへ変更
- 新規テスト`pgcancel_test.go`: レジストリの登録・照合・secret不一致・
  unregister後の無効化・キー一意性、**`connCancel`が「クエリ1をキャンセル
  してもクエリ2はキャンセルされたまま始まらない」ことを直接検証する
  回帰テスト**（今回のバグをピンポイントで固定化）、`-race`向けの並行
  アクセススモークテスト
- `tests/pgclient/main.go`: `checkCancelRequest`を追加——別接続の
  `conn.PgConn().CancelRequest(ctx)`で長時間クエリ（`WITH RECURSIVE`）を
  中断し、**中断後も同じ接続で次のクエリ（`SELECT 1`）が使えること**まで
  確認（`checkDisconnectDuringQuery`との違い——切断は接続を捨てる想定、
  `CancelRequest`は接続を維持したまま特定のクエリだけ中断する想定——を
  明示的にテストで区別）
- `.claude/rules/pgwire.md`:「クエリキャンセルの2系統」節へ全面改訂
  （クライアント切断時の自動キャンセルと真の`CancelRequest`を並記し、
  両者が`connCancel`という同じ仕組みを共有すること、発見した設計バグと
  修正方法を記録）
- **実機確認**: `tests/pgclient`（`CancelRequest`込み）が実サーバーに対し
  成功することを確認（修正前は`OK`が出ず、修正後`OK`が出ることを対比確認）
- `make check`・`make race`・`make test`（e2e、2回連続実行でflakinessなし）
  とも green を確認済み。依存追加なし

## フェーズ④Step 7（他言語ドライバ検証＋CI）完了（2026-09-04）

Python（psycopg2）・Node.js（node-postgres）・Java（pgJDBC）を`apt-get`/`npm`/
Maven Centralから都度インストール・取得し、それぞれ**自分自身のデフォルト
接続設定のまま**ExecDBのpgwireへ接続できることを実機で検証した。.NET
（Npgsql）は計画通り今回見送り（Extended Query実装済みのため接続自体は
可能なはずという前提を申し送り事項として記録するのみ）。

- **`tests/drivers/`（新規）**: `python/check.py`（psycopg2、`conn.commit()`/
  `conn.rollback()`経由で暗黙のBEGIN/COMMITを送らせる設計）・`node/check.js`
  ＋`package.json`＋`run.sh`（node-postgres、`$1`パラメータクエリで
  Extended Queryを明示的に使う）・`java/Check.java`＋`fetch-jdbc.sh`＋
  `run.sh`（pgJDBC、`PreparedStatement`経由）。各チェックは
  SELECT（int/float/text/bytea/NULL）・DDL拒否（SQLSTATE 42501）・
  INSERT+COMMIT+SELECTの基本往復を検証し、成功時`OK`を出力する
  （`tests/pgclient`と同じ規約）。Node依存（`node_modules/`）・Javaの
  pgJDBC jar（`lib/`）はgitignore済みの場所へ都度取得する設計とし、
  コミットしない（`.claude/rules/distribution.md`のバイナリ非コミット
  方針を踏襲、詳細は`tests/drivers/README.md`）
- **`tests/drivers/run-all.sh`（新規）**: 3チェックをまとめて実行する
  共有スクリプト。渡されたバイナリをスクラッチコピーし`.overwrite`で
  テーブル`t(a INTEGER)`を焼き込んでサーバーモードで起動、各ランタイムが
  `PATH`に無ければ`skip`表示のみで失敗扱いにしない（フェーズ③Step 6の
  PTY専用Ctrl+Cチェックと同じ既存パターン）。`tests/e2e.sh`と
  CI（下記`drivers`ジョブ）の両方から同一スクリプトを呼び、ロジックの
  二重化を避けた
- **`.github/workflows/test.yml`**: `drivers`ジョブを新設（`ubuntu-latest`
  限定——プロトコル実装自体はOS非依存であり、3ランタイム×3OSを揃える
  コストに見合わないため`check`の3OSマトリクスには含めない）。
  `setup-python`/`setup-java`/`setup-node`で各ランタイムを用意し、
  `make build`→`tests/drivers/run-all.sh`を実行。action参照は
  `testing.md`の教訓通り`git ls-remote`ではなくGitHub APIで実タグ名を
  事前確認（`setup-python@v7`/`setup-java@v6`/`setup-node@v7`——最初の
  見積もり`@v6`/`@v5`/`@v5`はいずれも誤りだった）
- **実機検証中に発見した、計画外の重大な相互運用性バグ（pgJDBC）**:
  pgJDBCをデフォルト設定のまま接続すると、`PreparedStatement.setInt`/
  `setDouble`等がBindメッセージのパラメータを**バイナリ形式**で送信し、
  ExecDB側は「バイナリ形式のパラメータは未対応」として一律拒否していた
  ため（フェーズ④Step 5の確定方針）、prepared statement経由のDML/SELECTが
  pgJDBCのデフォルト設定では全滅した（`binaryTransfer=false`を明示すれば
  回避可能——だがそれは「デフォルト設定のまま動く」というフェーズ④の
  完了条件に反する）。ユーザーに確認の上、**パラメータのバイナリ形式も
  実装する**方針を選択（結果値側でのpgxの前例——フェーズ④Step 5——と同型の
  判断）。
  - **原因の特定**: `handleParse`が`Parse`メッセージ自身のパラメータ型OID
    配列（`paramOIDs`）を読み捨てていたのが盲点だった。一時的なデバッグ
    出力で実際の`Parse`の中身を見たところ、pgJDBCは`setInt`に対し
    `Parse`の時点で自ら`OID 23`（int4）を申告しており、
    `ParameterDescription`が何を返すかとは無関係に、その自己申告した型に
    基づいてバインド形式を決めていることが判明した（実PostgreSQLでも
    同様の使い方が許容されているプロトコル本来の挙動）
  - **修正**: `preparedStatement`に`paramOIDs []uint32`を追加して`Parse`の
    申告を保持し、`handleBind`がバイナリ形式のパラメータを受け取った際は
    そのOIDを手がかりに`pgtype.go`の新設`decodeBinaryParam`でデコードする
    設計に変更（`int2`/`int4`/`int8`/`float4`/`float8`/`bool`/`bytea`/
    `timestamp`の8種。`int2`/`int4`/`float4`は結果値側の`columnOID`が
    一度も返さないOIDだが、クライアントが自己申告するパラメータ型としては
    独立に出現しうるため対応範囲に含めた）。OID未申告（0）のままバイナリで
    送ってきた場合はデコードのしようがないため従来通り拒否——
    `TestExtendedQueryRejectsBinaryFormatParameters`はこのケースの
    回帰テストとして残し、新設した
    `TestExtendedQueryAcceptsBinaryFormatParametersWithDeclaredOID`が
    今回のバグそのものの回帰テストになっている。`pgtype_test.go`にも
    `decodeBinaryParam`の純粋関数テストを追加
  - `execdb_spec.md`§8・`.claude/rules/pgwire.md`を、この発見の経緯
    （原因特定の過程を含む）とあわせて更新
- **実機確認**: `python3`/`node`/`java`をdevcontainerへ都度インストール
  （PLAN.md「保留事項」へ記録）した上で、3ドライバとも`tests/e2e.sh`・
  `tests/drivers/run-all.sh`単体実行の両方で成功を確認。`make check`・
  `make race`・`make test`（e2e、driversチェック込み）とも green
- 依存追加: Goの`go.mod`/`go.sum`は変更なし（`decodeBinaryParam`は標準
  ライブラリのみ）。Node/Javaの依存はいずれもgitignore済みでコミット対象外

## フェーズ④Step 8（仕様書・ルール・PLAN・devcontainer統合）完了（2026-09-04）

フェーズ④・全4フェーズの実装を締めくくる最終ステップ。Step 1〜7の実装過程で
仕様書・ルールファイルへの反映が追いつかず残っていた記述（特に「型マッピングは
実装時に確定する」「Extended Queryは将来の拡張候補」等、Step 2/5で既に解消済み
なのに書き換えられていなかった古い記述）を一掃した。

- **`execdb_spec.md`**:
  - §2: `SET`/`SHOW`が「許可」でも「DDLのように拒否」でもない**第3の区分**
    であることを明記する新節を追加
  - §8: 「型マッピング」節を全面書き換え——`affinityOID`の判定順序（`BOOL`/
    `DATE`|`TIME`を標準5分類より優先、標準5分類、NUMERIC catch-allは実行時Go
    動的型へのフォールバック）を表形式で確定記載。**古い「具体的な対応表は
    実装時に確定する」「Extended Queryへの対応は将来の拡張候補」という記述は
    実態と矛盾するため削除**（前者はStep 1〜2で、後者はStep 5で解消済み）。
    パラメータ側バイナリデコードの対応OID（8種）も明記。`CancelRequest`/
    `BackendKeyData`の新節、`SET`/`SHOW`シムのアクセス制御統合節への追記も実施
  - §9: `-u`/`--user`が`--help`（`printUsage`）と一致していることを確認
    （変更不要——実装が既に仕様書通りだった）
  - 冒頭の「既存ドライバ資産がそのまま接続できる」という主張に、実際に
    実機確認済みのドライバ（pgx/pgJDBC/psycopg/node-postgres）とNpgsql
    （未検証、Extended Query実装により接続可能なはずという申し送り）を明記
- **`.claude/rules/pgwire.md`**: 全面改訂。
  - 「型マッピング」節を確定内容に書き換え（旧「決め打ちで実装せず実際に
    繋いで確認すること」という指示形の記述を、確定した設計とその理由の
    説明へ）
  - `SET`/`SHOW`互換シムの新節を追加（Step 3実装済みだったが、これまで
    pgwire.mdに一度も記載されていなかった抜け漏れ）
  - フェーズ①Step 5の「`pgx`はsimple_protocol指定が必須」「全列text固定」
    という記述に、Step 5/2でそれぞれ解消済みであることを追記（削除はせず、
    実機確認で発覚した経緯という記録的価値は残しつつ、現状との矛盾を解消）
- **`.claude/rules/testing.md`**: 「他言語ドライバ検証の運用方針」節を新設
  （ランタイム未導入環境でのskip、CIジョブをOS別マトリクスに含めない理由、
  Node/Javaの依存を非コミットにする理由、Debian系`pip install`が
  `externally-managed-environment`で失敗する問題とapt経由での回避）
- **`.claude/rules/sqlite-quirks.md`**: フェーズ④Step 1のスパイクで判明した
  SQLite/`modernc.org/sqlite`固有の事実のうち未記録だったものを追記——
  `DatabaseTypeName()`が`decltype`を正規化せず返すこと、`ScanType()`が
  内部の先読みで`Next()`前でも正しい理由と空結果セットでの例外、
  `ScanType()`と実際の`Scan`結果の型が食い違いうること（`BOOLEAN`列の
  `bool`対`int64`）、型アフィニティ違反値の挙動、`DATE`系列の自動
  `time.Time`変換。加えて`$1`プレースホルダをSQLiteがネイティブに解釈できる
  という事実を新節として追加（Extended Query実装の前提になった発見）
- **`.claude/rules/naming.md`**: 対応表に`-u`/`--user`の行を追加
  （REPLコマンド・`engine` API双方が「─」になる、この表で唯一のCLI単独行。
  認証はREPLにかからず`engine`も認証の概念を持たないため、という理由を
  説明文に追記）
- **`.devcontainer/devcontainer.json`**: `postCreateCommand`に
  `python3-pip`/`python3-psycopg2`/`nodejs`/`npm`/`openjdk-17-jdk-headless`
  を追加（Step 7で都度手動インストールしていたもの）。「保留事項」の
  devcontainer反映待ちリストを空にした
- **確認**: `make check`・`make race`・`make test`とも green（ドキュメント・
  設定ファイルのみの変更のため回帰リスクは低いが、念のため実行）。
  `.devcontainer/devcontainer.json`のパッケージ列は`apt-get install --dry-run`
  で構文・パッケージ名の妥当性を確認済み（実際のdevcontainerリビルドは
  `.claude/rules/testing.md`「開発環境の前提」の方針通り、フェーズ途中では
  行わない）

## フェーズ④（PostgreSQL互換ワイヤープロトコル開発）完了

全8ステップ（スパイク→型マッピング→SET/SHOW互換シム→`--user`認証→
Extended Query→CancelRequest→他言語ドライバ検証＋CI→ドキュメント統合）を
完了し、外部I/F（PostgreSQL互換ワイヤープロトコル）は「主要ドライバが
デフォルト設定のまま、正しい型で、必要なら認証付きで接続・利用できる」
という当初のゴールを達成した。

**フェーズ④完了の判定（PLAN.md記載の基準、すべて満たした）**:
`make check`/`make race`/`make test`が通る／`pgx`・pgJDBC・node-postgres・
psycopgがデフォルト設定のまま接続しSELECT/DML/トランザクションが動く
（`tests/pgclient`・`tests/drivers/`で自動検証）／数値・BLOB列が各ドライバの
型付きAPIで読める／DDLが全ドライバから42501で拒否される／`-u`認証（正誤
パスワード・`EXECDB_PASSWORD`優先・`--no-repl`未設定時の起動中止）が動く／
別接続からの`CancelRequest`で実行中クエリが中断される／GitHub Actions
（3OS＋race＋trivy＋drivers）がgreen／仕様書と`.claude/rules/`が実装と
一致／devcontainer.jsonの反映待ちリストが空になる。**全項目達成。**

これにより、当初計画していた4フェーズ（①ミニマム実装→②`engine`ライブラリ
開発→③REPL開発→④PostgreSQL互換ワイヤープロトコル開発）すべてが完了した。
今後のドキュメント（`docs/`への再構成、`tour/`入門ガイド）は、フェーズ④
完了後の新しい作業として別途計画する
（`.claude/rules/directory-structure.md`が`tour/`を「実装完了後」に限定
している方針の通り）。

（フェーズ④への申し送り事項——型マッピング／`--user`認証／`CancelRequest`／
Extended Queryプロトコル——はStep 2〜7ですべて消化済み。フェーズ④自体の
完了サマリは上記「フェーズ④（PostgreSQL互換ワイヤープロトコル開発）完了」
節を参照。）

## 保留事項

- **devcontainer.json反映待ちリスト**: 現行コンテナはリビルドせずに開発を
  進める方針（都度手動でインストール・設定して進め、フェーズ④完了時に
  まとめて `devcontainer.json` へ反映する）。session内で手動インストール・
  設定を行った場合は、忘れずにここへ追記すること。
  - （なし。フェーズ④Step 7でインストールした`python3-pip`/`nodejs`+`npm`/
    `openjdk-17-jdk-headless`/`python3-psycopg2`は、フェーズ④Step 8で
    `.devcontainer/devcontainer.json`の`postCreateCommand`へ反映済み。
    Node/Javaの依存（`pg`パッケージ、pgJDBCのjar）は`tests/drivers/`配下の
    gitignore済みの場所へ都度取得する設計のため、コンテナへの恒久化対象外）
