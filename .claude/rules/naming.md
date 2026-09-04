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

`engine.OpenSelf()` / `engine.Open()` / `engine.New()` / `db.Session()` /
`db.LoadFrom()` はDBの生成・ロード・接続取得方法であり、ユーザー操作に直接
対応するコマンドではないため、REPLコマンド・CLI起動オプションの列が空欄に
なるのは意図通り（3レイヤー対応の原則が崩れているわけではない）。

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
