# CI/CD: 瞬時に立ち上がるテスト用DB

*English version: [ci-testing.md](ci-testing.md)*

考え方: スキーマとシードデータを最初から埋め込んだ実行ファイルを一度だけ
ビルドし、あとは各テストジョブがそれを実行するだけにする——DBコンテナも
マイグレーション手順も、サービスがhealthyになるのを待つ必要もない。

## 1. シード済みのスナップショットを一度だけビルドする

```sh
go install github.com/amisonnet8/execdb/cmd/execdb@latest

execdb <<'SQL'
CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT UNIQUE);
CREATE TABLE posts(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), title TEXT);
INSERT INTO users(name, email) VALUES ('Alice', 'alice@example.com'), ('Bob', 'bob@example.com');
INSERT INTO posts(user_id, title) VALUES (1, 'Hello World'), (1, 'Second post'), (2, 'Bob''s post');
.snapshot testdb
.exit
SQL
```

これで単独の実行ファイル`testdb`ができる——テスト用フィクスチャの
ディレクトリへコミットするか、後続のジョブがダウンロードするCIの
成果物ステップとしてビルドする。(習慣として*ソース*リポジトリへExecDBの
バイナリをコミットしないこと——これはテスト用フィクスチャという別の
ケースであり、リリースバイナリ自体をコミットしない理由は
[`.claude/rules/distribution.md`](../../.claude/rules/distribution.md)を
参照。)

## 2. テストジョブで使う

サーバーモードで実行し、アプリケーションの`DATABASE_URL`をそこへ向ける
——独立したコンテナも、ヘルスチェック待ちのステップも不要:

```sh
chmod +x testdb
./testdb -n -p 127.0.0.1:5432 -q &
SERVER_PID=$!

# テストスイート、例えば:
DATABASE_URL="postgres://any@127.0.0.1:5432/any" go test ./...

kill "$SERVER_PID"
```

データはメモリ上にあり、実行のたびに同じスナップショットから始まるため、
各ジョブはセットアップ時間ゼロで同一のまっさらなデータベースを得られる
——「Postgresコンテナを起動して待つ」というステップ自体が丸ごと消える。
ドライバごとの接続例は[アプリケーションから接続する](mock-server_ja.md)を
参照。

## 例: GitHub Actions

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Start test database
        run: |
          chmod +x fixtures/testdb
          ./fixtures/testdb -n -p 127.0.0.1:5432 -q &
      - name: Run tests
        env:
          DATABASE_URL: postgres://any@127.0.0.1:5432/any
        run: go test ./...
```

`services:`ブロックもイメージのpullもポート待ちループも無い——データ
ベースはジョブの中のただの1ステップになる。
