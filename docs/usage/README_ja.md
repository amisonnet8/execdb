# 使い方リファレンス

*English version: [README.md](README.md)*

ExecDBを日々使うための、索引的なリファレンスドキュメント。「調べもの」用の
テーブル中心の資料であり、ウォークスルー(手順を追った説明)ではない——
ExecDBが初めてなら[`docs/tour/`](../tour/)から始める方がよい。ある設計の
「なぜそうなっているか」(アクセス制御、永続化モデル、ワイヤープロトコル
内部の仕組み)については、
[`docs/spec/execdb_spec_ja.md`](../spec/execdb_spec_ja.md)を参照。

- [CLIオプション](cli-options_ja.md) — 起動フラグ全部(`-p`、`-n`、`-u`、...)
- [REPLコマンド](repl-commands_ja.md) — ドットコマンド全部(`.tables`、
  `.snapshot`、`.import`、...)

すぐ動かせる、タスク指向のウォークスルー(CIテスト用DB、バグ再現の共有、
モックAPIサーバー、他言語からの接続)については、
[`docs/examples/`](../examples/)を参照。

## 早わかり

```sh
# インストール(Go 1.26以降が必要)
go install github.com/amisonnet8/execdb/cmd/execdb@latest

# REPLを起動
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb> CREATE TABLE t(a INTEGER);
execdb> INSERT INTO t VALUES (1);
execdb> .snapshot mydb
Wrote mydb
execdb> .exit
```

`mydb`は、そのテーブルと行を埋め込んだ、単独で動く実行ファイルになって
いる——実行すると(Linux/macOSでは`./mydb`、Windowsでは`mydb.exe`)、
そのデータがそこにある。全リファレンスは[CLIオプション](cli-options_ja.md)と
[REPLコマンド](repl-commands_ja.md)を、次に何をするかは
[`docs/examples/`](../examples/)を参照。
