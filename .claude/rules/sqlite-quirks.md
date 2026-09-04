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

## `Deserialize`が伝播しない根本理由と、Backup APIによる解決（フェーズ②Step 1）

上記「`Deserialize`は呼び出したコネクションにしか反映されない」の**根本原因**が
フェーズ②Step 1で判明した。`Xsqlite3_deserialize`は内部で対象スキーマを
**匿名（名前なし）memdbストアとして開き直す**実装になっている
（`_memdbOpen`は名前が空文字の場合、複数コネクション間で共有される
グローバルリストに載せず、そのコネクション専用のprivateなstoreを作る）。
つまり**DSNをどう変えてもこの伝播しない挙動は直らない、設計上の必然**である。

**解決策: オンラインBackup APIを使う。** `modernc.org/sqlite`は
`(*conn).NewBackup(dstUri) / NewRestore(srcUri)`（`*Backup`型、`Step(n int32)`/
`Finish()`/`Remaining()`/`PageCount()`）というexportedなBackup APIを提供している
（`serialize`/`deserializer`と同じ`sql.Conn.Raw()`＋ローカルinterfaceで到達可能。
`*sqlite.Backup`型を名指しするため`modernc.org/sqlite`の非ブランクimportが必要）。

手順: ①捨てコネクション（`:memory:`など）に`Deserialize(blob)`で復元
→ ②そのコネクションから`NewBackup(生きているDBのDSN)`→`Step(-1)`（全ページ
コピー）→`Finish()`。これはSQLite本来のBtree/pager経由の複製であり、
`Deserialize`のような特殊経路を通らないため、**生きているDBの他のコネクション
（新規・既存問わず）に正しく伝播する**（`engine/session_spike_test.go`の
`TestSpikeBackupIntoLiveDatabase`/`TestSpikeBackupWhileDestinationHasIdleKeeper`で
実測確認）。`Finish()`はbackup先の一時コネクションを自動で閉じるため、
生きているDB側は**別途keeper相当のコネクションを常時1本保持しておかないと、
backup直後にストアごと解放されうる**点に注意。

## `cache=shared`インメモリDSNは読み取りが無期限・キャンセル不能にハングしうる（不採用）

フェーズ②Step 1で、`file:name?mode=memory&cache=shared`（共有キャッシュ）を
「生きているDB」の基盤として使う案を実測した結果、**致命的な欠陥**が見つかり
不採用とした。

共有キャッシュのテーブルロック衝突は`SQLITE_LOCKED_SHAREDCACHE`
（拡張コード262）という特別なエラーコードを返す。`modernc.org/sqlite`は
これに対して`sqlite3_unlock_notify`ベースの独自リトライ機構
（`conn.go`の`(*conn).retry()`）を実装しているが、この待機は生の
`sync.Mutex.Lock()`であり、**`context.Context`のキャンセルも、DSNの
`_busy_timeout`パラメータも一切見ない**。相手のトランザクションが将来
コミット/ロールバックされない限り、文字通り無限に待ち続ける
（`engine/session_spike_test.go`の`spikeRunHardBounded`ヘルパーのdocコメント、
および`TestSpikeTransactionIsolationBetweenSessions`/
`TestSpikeSerializeDuringConcurrentWriteTransaction`で実測）。

素朴に単一goroutineでこれを検証すると、Goランタイム自身の
「all goroutines are asleep - deadlock!」検出が発火してプロセスごと落ちる
（他に実行可能なgoroutineが無いとGoランタイムが本物のデッドロックと判定する
ため）。バックグラウンドgoroutine＋`select`+`time.After`で包んで初めて、
クラッシュさせずにハングを検出・記録できる。

**教訓:** SQLiteの「shared-cache modeはbusy handlerが呼ばれない」という
ドキュメント上の注意書きは、実際には「エラーが早く返る」ではなく
「無期限にハングしうる」ことを意味する。この違いは実測しないと分からない。

## `memdb` VFSは実ファイルDB相当のロック機構を持つ（有界・busy_timeoutが効く）

`modernc.org/sqlite`は`_sqlite3MemdbInit`により全プラットフォームで`memdb`
VFSを登録済みで、`file:/name?vfs=memdb`のようなDSNで使える
（`getVFSName()`がDSNの`vfs=`パラメータを解釈する）。**名前が`/`または`\`で
始まる場合、複数コネクション間でグローバルに同じストアを共有する**
（`_memdbOpen`が`nRef`参照カウント付きで管理）。`_busy_timeout`/`_timeout`
DSNパラメータにも対応済み（内部で`pragma busy_timeout=N`相当を実行）。

`memdb`のロック実装（`_memdbLock`）は、通常のファイルベースDBのSHARED/
RESERVED/EXCLUSIVEロックと同種の機構を使うため、ロック衝突時は通常の
`SQLITE_BUSY`を返し、**SQLiteの内蔵busy-handlerを正しく経由する**
（`_busy_timeout`で設定した時間だけ待ってから諦める、という有界な挙動を
`engine/session_spike_test.go`の`TestSpikeTransactionIsolationBetweenSessions`で
実測確認——設定した秒数ぴったり待ってからエラーが返ることを確認済み）。
`cache=shared`と違い、**有界でキャンセル安全**なので、フェーズ②はこちらを
「生きているDB」の基盤として採用した。

**ただし通常のファイルロックより粒度が粗い点に注意:** `_memdbLock`の実装を
読むと、書き込みロック（`FnWrLock > 0`）が立っている間は、**新規の`SHARED`
ロック取得（＝新しい読み取りの開始）もブロックされる**。通常のロールバック
ジャーナルモードなら`SHARED`と`RESERVED`は共存できる（既存の読み取りは
妨げられない）が、`memdb`はこの共存を実装していない、より単純化されたロック
モデルになっている。したがって、**オートコミットの単発`SELECT`は、他の
セッションが書き込みトランザクションを開いている間、`_busy_timeout`の
範囲内でブロックされうる**（フェーズ②のエンジン設計では、これを踏まえて
`_busy_timeout`を十分に長め——例えば数秒程度——に設定すること）。
既に確立済みの接続がすでに`SHARED`ロックを保持している場合
（`eLock <= 現在のFeLock`）は再取得が不要なため、この制約を受けない。

## インメモリDBでは`journal_mode=WAL`が使えない

`memdb`・`cache=shared`のどちらでも`PRAGMA journal_mode=WAL`を実行しても
`journal_mode`は`memory`のまま変化しない（フェーズ②Step 1で実測）。
真のMVCC（読み取りが書き込みをブロックしない・書き込みが読み取りを
ブロックしない）が欲しい場合の「WAL」という第三の選択肢は、インメモリDBには
存在しない。

## `Serialize()`はトランザクションを考慮しない（バリアが必要）

`Xsqlite3_serialize`はページ/メモリを単純にコピーするだけで、他コネクションの
未コミットトランザクションを意識しない。フェーズ②Step 1の実測では:

- `memdb`: 他セッションが未コミットの書き込みtx中に`Serialize()`を呼ぶと、
  **明示的なエラー**（`invalid length returned: -1`）になった（壊れたデータを
  静かに返すのではなく、失敗する側に倒れた——想定より安全寄りの結果）。
- `cache=shared`: 未コミット行が実際に`Serialize()`結果へ漏れることを確認
  （ただし`PRAGMA integrity_check`は`ok`のまま、壊れてはいないが一貫性の
  無いスナップショットになる）。

**対処: `Snapshot`/`Overwrite`は専用コネクションで`BEGIN IMMEDIATE`→
`Serialize()`→`ROLLBACK`というバリアを噛ませる。** `BEGIN IMMEDIATE`は
他に書き込み中のセッションがあれば（`_busy_timeout`の範囲で待った上で）
正しく拒否され、成功すればその時点で他の書き込みが無いことが保証されるため、
その状態で取った`Serialize()`は常に一貫したスナップショットになる
（`engine/session_spike_test.go`の`TestSpikeSerializeDuringConcurrentWriteTransaction`
の`barrierRefusesWhileWriterActive`/`barrierSucceedsAfterCommit`で実測確認）。

## `ResetSession`はトランザクションをロールバックしない

`database/sql`のコネクションプールがコネクションを再利用する際に呼ぶ
`(*conn).ResetSession`は、開いたままのトランザクションを**ロールバックしない**
（`conn.go`で確認）。そのため、プールから取得した`*sql.Conn`（`db.Exec`/
`Query`等の単発API経由）で`BEGIN`を実行し、`COMMIT`せずに`Close()`（＝プールへ
返却）すると、次にそのコネクションを引いた別の呼び出し元が意図せず開いた
ままのトランザクションを引き継いでしまう。**`BEGIN`/`COMMIT`/`ROLLBACK`は
必ず専有コネクション（`engine.Session`相当、プールを経由しないもの）でのみ
実行すること。**

## `ColumnTypes()`は`Next()`呼び出し前でも正しい値を返す

`sql.Rows.ColumnTypes()`は`Next()`を一度も呼んでいない状態で呼び出しても、
`DatabaseTypeName()`/`ScanType()`/`Nullable()`が正しい値を返すことを実測確認
（`INTEGER`/`REAL`/`TEXT`/`BLOB`/`NUMERIC`列、および式列——`i+1 AS expr`は
`dbType`が空文字列、`scanType`は`int64`——で確認）。「`Next()`前は不定になる
かもしれない」という事前の懸念（列の実体を読むまで型が確定しないSQLiteの
動的型付けの性質上）は杞憂だった。フェーズ④のPostgres OID型マッピング設計に
そのまま使える情報。

## `memdb`の実効サイズ上限は約960MiB付近

`SQLITE_MEMDB_DEFAULT_MAXSIZE`は1073741824（1GiB）だが、実際に大きな
BLOBを書き込んでいくと**約960MiB付近で`database or disk is full`
（`SQLITE_FULL`）になる**ことを実測確認（`engine/session_spike_test.go`の
`TestSpikeMemdbSizeCeiling`）。`cache=shared`は同じ実験で1.1GiB超まで
エラーにならなかったため、これは`memdb`特有の制約である。仕様書§7の
「2GB未満」という記述は不正確で、「約1GiB（`memdb`採用時）」に訂正が必要
（フェーズ②Step 6で対応）。
