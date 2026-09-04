# SQLite / modernc.org/sqlite の落とし穴

`engine`パッケージの実装（フェーズ①Step 1・Step 2）で実際に踏んだ、
`modernc.org/sqlite`および内部SQLiteエンジン特有の挙動をまとめる。仕様書
（`execdb_spec.md`）の記述だけでは判断できない・誤解しやすい実装上の注意点が
対象。新しい落とし穴に気づいたら、このファイルに追記すること。

## `Deserialize` は呼び出したコネクションにしか反映されない

`modernc.org/sqlite`が提供する`Serialize`/`Deserialize`（`execdb_spec.md`§7）は、
以下のシグネチャを持つ（v1.58.0で確認）。

```go
func (c *conn) Serialize() ([]byte, error)
func (c *conn) Deserialize(buf []byte) error
```

（スキーマ名引数は無い。常にmainスキーマ固定。`conn`型自体はunexportedだが、
メソッドはexportedなので `sql.Conn.Raw()` + ローカルinterfaceのアサーション
で到達できる。）

**落とし穴:** 共有キャッシュDSN（`file:execdb?mode=memory&cache=shared`）を
使っていても、`Deserialize`の結果は**呼び出したそのコネクションにしか
反映されない**。しかも「既に開いていた他のコネクションから見えない」だけで
なく、「`Deserialize`実行**後**に新規に開いたコネクション」からも見えない
（`engine/serialize_test.go`の`TestDeserializeDoesNotPropagateToOtherConnections`
で実測・回帰テストとして固定化済み）。

これは以下の理由で見落としやすい。

* 共有キャッシュDSN自体は、**通常のSQL文（`CREATE TABLE`/`INSERT`等）**については
  正しく複数コネクション間で伝播する（`engine_test.go`の
  `TestSharedCacheDSNIsVisibleToOtherConnections`で確認）。`Deserialize`だけが
  特別扱いで、SQL文の実行とは異なる経路（C API `sqlite3_deserialize`によるBtree
  差し替え）で動くため、シェアードキャッシュの通常の伝播機構に乗らない。
* 「共有キャッシュ＋通常SQL」と「`Deserialize`＋単一コネクション」を別々に
  検証しただけでは気づけない。**「共有キャッシュ＋`Deserialize`」という組み
  合わせを明示的にテストしないと発覚しない**（フェーズ①Step 1のスパイク
  テストではこの組み合わせを検証しておらず、Step 2で`engine.DB`を実装した
  際に初めて発覚した）。

**対処方針（`engine`パッケージでの実装）:** `DB.Exec`/`Query`/`QueryRow`は、
コネクションプール（`db.sdb`）ではなく、`Open`/`OpenSelf`/`Load`が
`Deserialize`を実行したのと同じ単一のコネクション（`db.keeper`）経由に統一する。
`Load`も既存のコネクションに`Deserialize`し直すのではなく、新しい`*sql.DB`+
`*sql.Conn`のペアをまるごと作り直し、その新しいコネクション上で`Deserialize`
してから`db.keeper`を差し替える。

**今後の設計への影響:** フェーズ③/④で`cmd/execdb`がREPLと外部I/F（pgwire）を
「2つの独立したクライアント」として扱う際（仕様書§2/§8）、両方が素朴に
別々の`*sql.Conn`を持つ設計のままだと、起動時`OpenSelf()`や`.load`で取り込んだ
データがpgwire側のセッションから見えない、という事故が起きうる。着手時に
`PLAN.md`の該当セクション（「Step 2で確定した事実」）を確認し、対策
（`engine.DB`のAPI経由に統一する／SQLiteのBackup/Restore API等で伝播させる）
を検討すること。

## `Deserialize`後のDBは（フラグが正しく設定されていれば）拡張可能

`modernc.org/sqlite`は内部で`sqlite3_deserialize()`を呼ぶ際、
`SQLITE_DESERIALIZE_RESIZEABLE|SQLITE_DESERIALIZE_FREEONCLOSE`を指定済み
（`conn.go`で確認）。そのため、復元直後のDBに対して大量の追加`INSERT`を
行っても`database or disk is full`のようなエラーにはならない
（`engine/serialize_test.go`で2万行規模の追加書き込みを確認済み）。

自前でSQLiteをラップする場合や、将来ライブラリのバージョンが変わった場合は、
このフラグが維持されているかどうかを再確認すること（フラグが無いと復元後の
DBが実質的に読み取り専用・固定サイズになる）。
