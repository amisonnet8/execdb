# 1. 第1歩: REPL

*English version: [01-first-steps.md](01-first-steps.md)*

引数無しで起動すると、空のインメモリデータベースを背後に持つ、対話式の
SQLコンソールが得られる:

```sh
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb>
```

`.`で始まらないものは何でもそのままSQLとして扱われる——本物のSQLite
であり、おもちゃのサブセットではない。テーブルを作って、いくつか行を
入れてみる:

```
execdb> CREATE TABLE todos(id INTEGER PRIMARY KEY, task TEXT, done INTEGER DEFAULT 0);
execdb> INSERT INTO todos(task) VALUES ('write the tour'), ('ship it');
execdb> SELECT * FROM todos;
1|write the tour|0
2|ship it|0
```

この`|`区切りでヘッダの無い出力が`.mode list`、つまりデフォルトの形式
——`grep`/`awk`/スクリプトへパイプすることを前提にしている。ターミナルで
もっと読みやすくしたいなら、モードを切り替える:

```
execdb> .mode column
execdb> .headers on
execdb> SELECT * FROM todos;
id  task            done
--  --------------  ----
1   write the tour  0
2   ship it         0
```

`.mode column`はヘッダを自動でonにするため、上の1行だけで両方が有効に
なった。他にも3つの出力モード(`csv`、`json`、`line`)があり、結果を
他のプログラムへ渡す用途向けになっている——全モードの詳細は
[REPLコマンド](../usage/repl-commands_ja.md#出力の書式)を参照。

自分で今打ったのではないスキーマを見て回るための2つのコマンド:

```
execdb> .tables
todos
execdb> .schema todos
CREATE TABLE todos(id INTEGER PRIMARY KEY, task TEXT, done INTEGER DEFAULT 0);
```

そして終了する:

```
execdb> .exit
```

この時点で、やったことはすべて消えている——ディスク上のどこにも
データベースファイルは無く、ExecDBは決して自動保存しない。これは
回避すべき制限ではなく、まさにこの仕組みの要点そのものであり、次の章の
テーマになる。

**次へ:** [2. スナップショット: バイナリそのものがデータになる](02-snapshots_ja.md)
