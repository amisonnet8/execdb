# バグ再現データ(や任意のデータ状態)を実行可能なファイルとして共有する

*English version: [snapshot-sharing.md](snapshot-sharing.md)*

データベース中の特定のデータでしか再現しないバグの場合、よくある解決策は
セットアップ手順の壁("この12個のSQL文を、この順番で実行して...")になる。
ExecDBなら*データベースそのもの*を共有できる。

## 状態をキャプチャする

バグを再現した状態のまま:

```
execdb> .snapshot bug_123
Wrote bug_123
```

`bug_123`をチームメイトへ送る(Slack、共有ドライブ、issueへの添付——ただの
ファイルなので)。手順書は要らない。

## 再現する

受け取った側は、他の実行ファイルと全く同じように実行するだけでよい:

```sh
chmod +x bug_123        # Windowsでは不要
./bug_123
```

```
ExecDB v...
Loaded snapshot: bug_123
Enter ".help" for usage hints.
execdb>
```

バグが起きたときと同じテーブル、同じ行、同じ状態——インストールも
dump/restore手順も、Postgresのバージョン不一致のデバッグも不要。

## クロスプラットフォームでの共有

`.snapshot`/`.overwrite`は常に現在実行中の実行ファイルに対してのみ作用
するため、Linux上で取ったスナップショットはLinuxバイナリになり、
Windowsでは動かない。*データ*(エンジンではなく)を別のOS/アーキテクチャへ
移す場合:

```sh
# 対象OS上で、そのプラットフォーム向けの空のExecDBバイナリから始める:
execdb-windows-amd64.exe
execdb> .load bug_123        # bug_123のデータだけを取り込む(エンジンではない)
execdb> .overwrite            # そのデータをexecdb-windows-amd64.exe自身へ書き込む
```

`.load`は指定したファイルに埋め込まれた*データ*だけを読み込み、そのファイル
のエンジンコードは決して読み込まない——そのため、ExecDBがビルドされている
どの組み合わせのプラットフォーム間でもこの手順が使える。
`.snapshot`/`.load`/`.overwrite`の完全なリファレンスは
[CLIオプション](../usage/cli-options_ja.md)と
[REPLコマンド](../usage/repl-commands_ja.md)を参照。
