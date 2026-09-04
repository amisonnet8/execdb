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
   `examples/e2e.sh`、`examples/pgclient`（pgx、examples専用依存）、
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
`examples/pgclient`（pgx）の両方から疎通しDDL系が42501で拒否される／
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

## 現在地

*（このセクションは実装が進むにつれて更新すること。「今どのフェーズの、
どの部分に着手しているか」を都度書き残しておくと、セッションをまたいだ
ときに文脈を復元しやすい。）*

- 現在のフェーズ: ①ミニマム実装 — **Step 2完了**、Step 3（`cmd/execdb` CLI・
  バナー・REPL）着手前
- 下準備として以下を作成済み（2026-09-03時点）:
  - `go.mod`（module: `github.com/amisonnet8/execdb`）
  - `LICENSE`（MIT, copyright: amisonnet8）
  - `.gitignore`
  - `README.md` / `README_ja.md`
  - `examples/README.md`（用途の説明のみ、中身は未整備）
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
- 次のアクション: Step 3（`cmd/execdb` — CLI起動オプション、起動時バナー、
  REPL、ドットコマンド）に着手する。特に`.overwrite`のREPL経由での実機確認を
  優先する。

## 保留事項

（なし。`Makefile`・自動フックともにStep 1で確定・設定済み。）
