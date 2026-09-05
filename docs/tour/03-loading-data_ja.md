# 3. データの出し入れ

*English version: [03-loading-data.md](03-loading-data.md)*

## CSVを読み込む

`.import`はCSVファイルをテーブルへ一括で読み込む——まだ存在しなければ
ヘッダ行からテーブルを作成する:

```
execdb> .import sample.csv students
Inserted 3 rows into "students".
execdb> SELECT * FROM students;
Alice|90
Bob|75
Carol|88
```

作られるテーブルは全列`TEXT`になる(CSVのヘッダ行から得られるのはそれが
限界)——数値が必要なら`SELECT`側で`CAST`や算術演算をすればよい。列数が
行ごとに合わない場合は、一部だけスキップするのではなく、インポート全体を
中断する——正確なルールは
[REPLコマンド](../usage/repl-commands_ja.md#データの保存と読み込み)を
参照。

## SQLとしてダンプし直す

```
execdb> .dump
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE "students"("name" TEXT,"grade" TEXT);
INSERT INTO "students" VALUES('Alice','90');
INSERT INTO "students" VALUES('Bob','75');
INSERT INTO "students" VALUES('Carol','88');
COMMIT;
```

これはそのまま再実行可能な、プレーンなSQLテキストだ——別の`execdb`
プロセスへパイプする(または`.sql`ファイルとして保存する)ことで、同じ
状態を別の場所で再現できる。`.dump`は、一部のテーブルだけが欲しい場合の
`LIKE`パターンもオプションで受け付ける。

## 別のスナップショットのデータを取り込む

`.load`はスナップショットを直接実行するのとは違う: 今開いている
データベースの**インメモリのデータだけ**を、**今実際に動いている
エンジン**を使って(読み込み元のファイルに埋め込まれているエンジンでは
なく)置き換える。

```
execdb> .load mydb
Loaded data from mydb
execdb> SELECT * FROM todos;
1|write the tour|0
2|ship it|0
3|take a break|1
```

この違いが効いてくるのは、時間をかけていくつも(あるいは受け取った複数の)
スナップショットを作ってしまい、いくつもの別々の実行ファイルを行き来する
のではなく、1つのエンジンの下でそれらのデータをマージ・比較・移行したい
場合だ。`.load`自体はディスクに何も書き込まない——結果を残したければ
続けて`.snapshot`/`.overwrite`を実行する。

**次へ:** [4. 他のツールから話しかける](04-external-connections_ja.md)
