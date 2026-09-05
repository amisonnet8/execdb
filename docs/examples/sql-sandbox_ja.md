# 環境構築ゼロのSQLサンドボックス

*English version: [sql-sandbox.md](sql-sandbox.md)*

SQLを学ぶ、クエリのアイデアを試す、あるいは授業/勉強会でちょっとした
演習をする——そんなときに、ExecDBは単独のバイナリ以外に何もインストール
することなく、フル機能のSQLエンジン(view、index、trigger、transaction
——おもちゃのサブセットではなく本物のSQLite)を提供する。

[最新リリース](https://github.com/amisonnet8/execdb/releases/latest)から
自分のプラットフォーム用のバイナリを取得する(Go不要)、または
Go 1.26以降があれば`go install github.com/amisonnet8/execdb/cmd/execdb@latest`
でもよい:

```sh
execdb
```

```
ExecDB v...
No embedded data. Starting with an empty in-memory database.
Enter ".help" for usage hints.
execdb> CREATE TABLE students(id INTEGER PRIMARY KEY, name TEXT, grade INTEGER);
execdb> INSERT INTO students(name, grade) VALUES ('Alice', 90), ('Bob', 75), ('Carol', 88);
execdb> SELECT name FROM students WHERE grade >= 85 ORDER BY grade DESC;
Alice
Carol
execdb> .mode column
execdb> .headers on
execdb> SELECT name, grade, CASE WHEN grade >= 90 THEN 'A' WHEN grade >= 80 THEN 'B' ELSE 'C' END AS letter FROM students;
name   grade  letter
-----  -----  ------
Alice  90     A
Bob    75     C
Carol  88     B
```

すべてメモリ上にあり、終了すると消える——`DROP DATABASE`での片付けも、
コンテナの残骸も、次の人が同じ端末を使う前にリセットすべき状態も無い。
作ったものを残しておきたければ、1つのコマンドでファイルに変えられる:

```
execdb> .snapshot my_lesson
Wrote my_lesson
execdb> .exit
```

`./my_lesson`(Windowsでは`my_lesson.exe`)は、どのマシンで実行しても
まさに続きから始まる——インストール不要、セットアップ不要、
Windows/macOS/Linuxのどれでもターミナルからでもダブルクリックからでも
同じように動く。完全なコマンドリファレンスは
[REPLコマンド](../usage/repl-commands_ja.md)を参照。試すためのCSV
データセットを読み込む`.import`や、丸ごとSQLテキストとして出力し直す
`.dump`も含まれている。
