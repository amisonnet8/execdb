# 2. スナップショット: バイナリそのものがデータになる

*English version: [02-snapshots.md](02-snapshots.md)*

これが、ExecDBを「ただSQLiteを動かすだけ」とは違うものにしている、
たった1つの考え方だ: **永続化とは、隣に`.db`ファイルを置くことではなく、
新しい実行ファイルを書き出すこと**である。1つのコマンドでそれができる:

```
execdb> CREATE TABLE todos(id INTEGER PRIMARY KEY, task TEXT, done INTEGER DEFAULT 0);
execdb> INSERT INTO todos(task) VALUES ('write the tour'), ('ship it');
execdb> .snapshot mydb
Wrote mydb
execdb> .exit
```

`mydb`は、どこか別の`execdb`プログラムが読み込むデータベースファイルでは
ない——テーブルと行がそのまま焼き込まれた、エンジン自体の完全で単独な
コピーである:

```sh
chmod +x mydb   # Windowsでは不要
./mydb
```

```
ExecDB v...
Loaded snapshot: mydb
Enter ".help" for usage hints.
execdb> SELECT * FROM todos;
1|write the tour|0
2|ship it|0
```

`mydb`を別のマシン、Dockerイメージ、CIジョブへコピーするだけでよい——
他にインストールも、マウントも、設定も要らない。ただの普通の実行ファイル
なので、そのように扱えばよい(コミットする、`scp`する、バグレポートに
添付する)。

## スナップショットをその場で編集する

データを追加してから、`.overwrite`で今実行しているファイル自身へ
折りたたむ:

```
execdb> INSERT INTO todos(task, done) VALUES ('take a break', 1);
execdb> .overwrite
Overwrote the running executable.
```

`.overwrite`は完了すると自動的に終了する——その後に別途`.exit`を打つ必要
は無い。もう一度`./mydb`を実行すると、新しい行もそこにある。

## 自動では起きないこと

`.exit`/`.quit`は保存確認を出さず、自動保存もしない——`.snapshot`/
`.overwrite`を実行していなければ、変更は消える。これは意図的な設計であり
(スクリプトやCIの実行が保存プロンプトでブロックされるべきではない)、
機能の不足ではない。長いセッションに対する安全網が欲しければ、代わりに
`-i`/`--snapshot-interval`がタイマーで保存してくれる——詳細は
[CLIオプション](../usage/cli-options_ja.md#定期スナップショット)を参照。

**次へ:** [3. データの出し入れ](03-loading-data_ja.md)
