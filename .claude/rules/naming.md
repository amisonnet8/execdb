# 命名規則

ExecDB では、REPLコマンド・CLI起動オプション・`engine`パッケージのGo API名を、
できる限り一致させる方針を採る。過去に `Save()`/`SaveSelf()` という命名が
コマンド名（`.snapshot`/`.overwrite`）とずれていたことがあり、これを
`Snapshot()`/`Overwrite()` に統一した経緯がある。新しい機能を追加する際も
同様に、3つのレイヤーで名前を揃えること。

## 対応表（現在の一覧）

| REPLコマンド | CLI起動オプション | `engine` API |
| :--- | :--- | :--- |
| `.snapshot` | `--snapshot-as` | `db.Snapshot(path string)` |
| `.overwrite` | ─（自身のパスに固定） | `db.Overwrite()` |
| `.load` | ─（REPL専用、CLI起動オプションなし） | `db.Load(path string)` |
| ─（起動時に暗黙実行） | ─ | `engine.OpenSelf()` |
| ─（ライブラリ専用、REPL/CLIの対応なし） | ─ | `engine.Open(path string)` / `engine.New()` |
| ─（ライブラリ専用。`cmd/execdb`のREPLは起動時に1本、pgwireはTCP/UDS1接続につき1本を保持する） | ─ | `db.Session(ctx context.Context)` |
| ─（ライブラリ専用、`.load`のio.Reader版） | ─ | `db.LoadFrom(r io.Reader)` |
| ─（ライブラリ専用。REPLの内部実装（`completeStatements`）が入力が完結したか判定するのに使う） | ─ | `engine.Complete(sql string) (bool, error)` |
| ─（ライブラリ専用。`.import`のCSV一括投入が同じ文を繰り返し実行するのに使う） | ─ | `s.Prepare(query string)` / `s.PrepareContext(ctx, query string)` |
| `.mode` | ─ | ─ |
| `.headers` | ─ | ─ |
| `.dump` | ─ | ─ |
| `.import` | ─ | ─ |
| ─（自動実行、対応するREPLコマンドなし） | `-i` / `--snapshot-interval` | `db.Snapshot(path string)`（`.snapshot`と共用） |
| ─（REPLには認証をかけない。外部I/Fのみ） | `-u` / `--user` | ─（`engine`は認証の概念を持たない。`cmd/execdb/auth.go`で完結） |

`engine.OpenSelf()` / `engine.Open()` / `engine.New()` / `db.Session()` /
`db.LoadFrom()` はDBの生成・ロード・接続取得方法であり、ユーザー操作に直接
対応するコマンドではないため、REPLコマンド・CLI起動オプションの列が空欄に
なるのは意図通り（3レイヤー対応の原則が崩れているわけではない）。

`.mode`/`.headers`/`.dump`/`.import`はいずれもCLI起動オプション・`engine` API
の対応を持たない、`cmd/execdb`内で完結する REPL 専用コマンドである
（フェーズ③のスコープ判断: スキーマ内省・出力整形・CSV変換のロジックを
`engine`側のAPIとして切り出さない、という確定方針。`.import`が使う
`s.Prepare`/`s.PrepareContext`のみが例外的に`engine`への追加になった）。
`-i`/`--snapshot-interval`は対応するREPLコマンドを持たない代わりに、
`.snapshot`と同じ`db.Snapshot(path string)`を定期的に呼ぶだけであり、
専用の`engine` APIを新設していない。

`-u`/`--user`（フェーズ④Step 4）はREPLコマンド・`engine` APIのどちらとも
対応しない、この表で唯一「CLIオプションだけが単独で存在する」行である。
認証は外部I/F（pgwire）の接続時にのみかかり、REPL自体には認証をかけない
という設計（`execdb_spec.md`§8）のため対応するREPLコマンドが無く、
「ユーザー」という概念自体を`engine`が持たない（`db.Session(ctx)`はどの
クライアントにも区別なく専有コネクションを返すだけ）ため対応する`engine`
APIも無い。認証ロジックは`cmd/execdb/auth.go`に閉じている。

新規追加時も、この3レイヤーの名前が対応するように設計すること。対応関係が
崩れる変更をする場合は、必ず3箇所（REPL・CLI・`engine`）を同時に確認・修正する。

## 短縮フラグ（CLI起動オプション）

- 短縮フラグは1文字、**すべて小文字**で統一する（`-H`/`-S` のような大文字は使わない）。
- `-h` は `--help` の定位置として予約済み。他のオプションで `-h` を使わないこと
  （例: PostgreSQL待受アドレスは `-H` ではなく `-p`/`--pg-addr` とした）。
- 長い形式（`--xxx`）と短縮形式（`-x`）は必ずセットで用意する。

## ファイル名生成（タイムスタンプ付与）

`.snapshot` や `--snapshot-as` で生成するファイル名にタイムスタンプ
（`_YYYYMMDDHHMMSS`）を付与する際のルールは以下の1つに統一されている。
新しい保存系コマンド・オプションを追加する場合も、このルールを踏襲すること。

1. ベースとなるファイル名（拡張子を除く部分）から、既存の `_YYYYMMDDHHMMSS`
   パターンがあれば取り除く（二重付与を防ぐ）。
2. 拡張子の直前に新しい `_YYYYMMDDHHMMSS` を挿入する。
3. Windows環境で拡張子が省略されている場合は `.exe` を付与する。

`--timestamp`（`-t`）は bool フラグ（付与する/しない）であり、`auto`/`always`/`never`
のような複数値の選択肢は持たない（過去にこの3値方式を採用していたが、
ルールを1本化した際に廃止した）。
