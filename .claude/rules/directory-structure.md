# ディレクトリ構成

ExecDBは「`engine`ライブラリが核、現行のスタンドアロン実行ファイル
（REPL＋外部I/F＋バイナリ内蔵）はそのライブラリを使った1つのアプリケーション」
という構造を取る（仕様書§6参照）。新しいファイルを追加する際は、必ずこの
構造のどちらに属するかを意識すること。

```
execdb/
├── CLAUDE.md               ← プロジェクトルール（参照先の案内）
├── execdb_spec.md          ← ExecDBの仕様（ドラフト版、実装しながら育てる）
├── PLAN.md                 ← 実装計画・進捗管理（開発フェーズ、現在地、保留事項）
├── Makefile
├── LICENSE                 （MIT）
├── go.mod
├── go.sum
├── .gitignore
├── README.md
├── README_ja.md
├── .devcontainer/
├── .vscode/
├── .claude/
│   ├── settings.json
│   └── rules/
├── engine/                 ← 【ライブラリ本体】インメモリSQLエンジン
├── cmd/execdb/             ← 【アプリ】engineを利用した単一バイナリRDBMSの実装
├── tests/                    ← `make test` が実行するE2E/結合テスト一式
├── tour/                     ← 入門ガイド（実装完了後に作成予定）
└── .github/workflows/       ← CI定義（test.yml, release.yml 等）
```

`engine/`・`cmd/execdb/` 配下の具体的なファイル構成（`engine.go`, `persist.go`,
`main.go`, `repl.go`, `pgwire.go` 等）は、実装を進めながら決めてよい。ここでは
「どちらのディレクトリに属するか」という配置の判断基準のみを定める。

## 配置の判断基準

- **ネットワークI/Fに一切触れないコード** → `engine/`
- **REPL・外部I/F・アクセス制御・CLIオプションなど、`engine`を使う側のロジック** → `cmd/execdb/`
- `engine`パッケージから `net`/`net/http` 等への依存を追加しないこと
  （§6の「ネットワークI/Fを持たないことによる安全性」という設計原則を壊すため）。

## 各ファイル・ディレクトリの補足

- **go.sum**: リポジトリにコミットする。依存パッケージのハッシュ値による
  改ざん検知・再現可能なビルドを担保するファイルであり、`go.mod` とセットで
  コミットするのがGoの標準的な慣習。除外しない。
- **README.md / README_ja.md**: 英語版・日本語版を両方用意する。
- **.devcontainer/**: 開発コンテナ定義（`devcontainer.json` 等）。
- **.vscode/**: エディタ設定（推奨拡張機能等、チームで共有したい設定のみ）。
- **tour/**: 実装が完了してから作成する入門ガイド。実装フェーズ（①〜④、
  `PLAN.md`参照）の途中では着手しない。
- **tests/**: `.claude/rules/testing.md` の `make test` が実行するE2E/結合
  テスト一式（`e2e.sh`、他言語ドライバ確認用の `pgclient` 等）。ユーザー向けの
  使い方サンプルではなく、テスト専用のディレクトリと位置づける（ユーザー向け
  サンプルをどう用意するかは、フェーズ④完了後にあらためて検討する）。
- **配布物（各OS向けビルド済みバイナリ）**: リポジトリに直接コミットしない
  （詳細は `.claude/rules/distribution.md` 参照）。
