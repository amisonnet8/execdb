// ExecDB PoC: 「実行ファイル自体にデータを持ち、.save で新しい別名実行ファイルとして
// スナップショットを生成できるか」に加え、「自分自身を上書きして終了する」ケースを検証する。
//
// 仕様書 §7 の固定長フッター方式をそのまま踏襲:
//   [エンジン部分] [データブロブ] [フッター(固定32バイト)]
//   フッター = Magic(8) + Version(4) + DataOffset(8) + DataLength(8) + Reserved(4)
//
// 使い方:
//   go build -o execdb_poc execdb_poc.go
//   ./execdb_poc                                   # 初回起動。データなしのはず
//   ./execdb_poc --save "hello world" gen1          # gen1 という別名ファイルにテキストを埋め込んで生成
//   ./gen1                                          # 実行すると "hello world" が読めるはず
//
//   # 自分自身を上書きするケース（今回追加）
//   ./gen1 --save-self "overwritten data"           # gen1 自身を書き換えて即終了
//   ./gen1                                          # "overwritten data" になっているはず（ファイル名は変わらない）
//
// なぜ素朴な上書きが失敗するか:
//   os.Executable() で得た自分自身のパスに対して os.OpenFile(path, O_WRONLY|O_TRUNC, ...) すると、
//   Linuxでは "text file busy" (ETXTBSY) で失敗する。実行中のバイナリの中身を直接書き換えることは
//   カーネルが禁止しているため。Windowsではさらに厳しく、実行中のファイルへのrenameのターゲット
//   指定すら ERROR_SHARING_VIOLATION で拒否される。
//
// 回避策（本ファイルの実装方針・Linux/Windows両対応）:
//   1. 実行中の自分自身を selfPath -> selfPath+".execdb_old" にrenameで退避する
//      → 「実行中ファイルを別名にrenameする」操作自体はLinux/Windowsどちらでも通る
//   2. 空いた元のパス(selfPath)へ新しい中身を新規書き込みする
//      → 既存ファイルへの上書きではなく新規作成になるため、どちらのOSでも通る
//   3. 退避ファイル(.execdb_old)の削除を試みる
//      → Linux: 実行中でもunlinkできるためその場で消える
//      → Windows: 実行中はロックされていて削除できないのが正常。次回起動時に掃除する
//
// 注意:
//   Windowsでの削除待ちファイル（.execdb_old）は、次回起動時に cleanupOrphanedOldSelf で
//   ベストエフォートに掃除される。何らかの理由でプロセスが二度と起動されなければ
//   .execdb_old が残り続けるが、動作に支障はない（ゴミファイルとして手動削除も可能）。
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	magic      = "EXECDB01"
	footerSize = 32 // magic(8) + version(4) + offset(8) + length(8) + reserved(4)
)

// loadSelfData は自分自身の実行ファイルの末尾からフッターを探し、
// 埋め込みデータとエンジン部分のサイズ（= データがない場合はファイル全体サイズ）を返す。
func loadSelfData(selfPath string) (data []byte, engineSize int64, err error) {
	f, err := os.Open(selfPath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := stat.Size()

	if size < footerSize {
		// フッターすら入らないサイズ = データなし
		return nil, size, nil
	}

	footer := make([]byte, footerSize)
	if _, err := f.ReadAt(footer, size-footerSize); err != nil {
		return nil, 0, err
	}

	if string(footer[0:8]) != magic {
		// マジックバイトが見つからない = データなしの素のバイナリ
		return nil, size, nil
	}

	// version := binary.BigEndian.Uint32(footer[8:12]) // 今回は未使用
	dataOffset := binary.BigEndian.Uint64(footer[12:20])
	dataLength := binary.BigEndian.Uint64(footer[20:28])

	buf := make([]byte, dataLength)
	if _, err := f.ReadAt(buf, int64(dataOffset)); err != nil {
		return nil, 0, err
	}

	return buf, int64(dataOffset), nil
}

// buildSnapshotBytes は「エンジン部分 + 新データ + 新フッター」の完成形バイト列を組み立てる。
func buildSnapshotBytes(selfPath string, engineSize int64, newData []byte) ([]byte, error) {
	engineBytes := make([]byte, engineSize)
	f, err := os.Open(selfPath)
	if err != nil {
		return nil, err
	}
	if _, err := f.ReadAt(engineBytes, 0); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	footer := make([]byte, footerSize)
	copy(footer[0:8], []byte(magic))
	binary.BigEndian.PutUint32(footer[8:12], 1) // version
	binary.BigEndian.PutUint64(footer[12:20], uint64(engineSize))
	binary.BigEndian.PutUint64(footer[20:28], uint64(len(newData)))
	// footer[28:32] は Reserved（ゼロのまま）

	out := make([]byte, 0, len(engineBytes)+len(newData)+footerSize)
	out = append(out, engineBytes...)
	out = append(out, newData...)
	out = append(out, footer...)
	return out, nil
}

// saveSnapshot は「エンジン部分 + 新データ + 新フッター」を新しい別名ファイルへ書き出す。
// outPath が selfPath と異なる（＝別名保存）ケース専用。
func saveSnapshot(selfPath string, engineSize int64, newData []byte, outPath string) error {
	blob, err := buildSnapshotBytes(selfPath, engineSize, newData)
	if err != nil {
		return err
	}

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := out.Write(blob); err != nil {
		return err
	}
	return nil
}

// oldSuffix は「退避させた旧自分自身」の一時的なファイル名に付ける接尾辞。
const oldSuffix = ".execdb_old"

// saveSnapshotSelf は「自分自身のファイルを新しい中身に差し替える」ケース専用。
//
// Linux: selfPathへ新しいtmpファイルをrenameで直接上書きするだけでもETXTBSYを回避できるが、
// Windowsでは「実行中のファイルをrenameのターゲットにする」こと自体が
// ERROR_SHARING_VIOLATIONで拒否されるため、この方式はWindowsで使えない。
//
// 代わりに、両OSで共通して通る「自分自身を別名へ退避(rename) → 空いた元の名前に新規書き込み」
// という手順を採る。
//   1. selfPath -> selfPath + oldSuffix にrename（自分自身の退避。実行中でも通る）
//   2. 空いたselfPathへ新しい中身を新規書き込み（既存ファイルではないのでどちらのOSでも通る）
//   3. oldSuffixファイルの削除を試みる（Linuxは即成功。Windowsは実行中なので失敗するのが正常）
func saveSnapshotSelf(selfPath string, engineSize int64, newData []byte) error {
	blob, err := buildSnapshotBytes(selfPath, engineSize, newData)
	if err != nil {
		return err
	}

	oldPath := selfPath + oldSuffix

	// 前回のクリーンアップ漏れ（Windowsで前回削除できなかった.oldが残っている等）に備えて、
	// 万一既に存在していたら先に消しておく（存在しなくてもエラー無視でよい）。
	os.Remove(oldPath)

	// 1. 自分自身を退避。実行中のファイルをrenameで「別名にする」操作自体はLinux/Windows双方で通る。
	if err := os.Rename(selfPath, oldPath); err != nil {
		return fmt.Errorf("自分自身の退避に失敗: %w", err)
	}

	// 2. 空いた元の名前へ新規書き込み。
	if err := os.WriteFile(selfPath, blob, 0o755); err != nil {
		// 書き込みに失敗したら退避を元に戻しておく（ベストエフォート）
		os.Rename(oldPath, selfPath)
		return fmt.Errorf("新しい中身の書き込みに失敗: %w", err)
	}

	// 3. 退避ファイルの削除を試みる。
	//    Linux: 実行中でもunlinkできるためここで消える。
	//    Windows: 実行中はロックされて削除できないのが正常なので、エラーは無視する。
	//             次回起動時に cleanupOrphanedOldSelf で掃除する。
	_ = os.Remove(oldPath)

	return nil
}

// cleanupOrphanedOldSelf は、前回の .save-self 実行時にWindowsで削除できなかった
// 退避ファイル（実行中ロックが外れた今は削除可能）を、起動時にベストエフォートで掃除する。
func cleanupOrphanedOldSelf(selfPath string) {
	oldPath := selfPath + oldSuffix
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err == nil {
			fmt.Printf("[起動] 前回の退避ファイルを削除しました: %s\n", oldPath)
		}
		// 削除に失敗しても致命的ではないので無視（次回起動時にまた試みる）
	}
}

func main() {
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Println("os.Executable() エラー:", err)
		os.Exit(1)
	}

	// 前回 .save-self 実行時にWindowsで削除しきれなかった退避ファイルがあれば掃除する
	cleanupOrphanedOldSelf(selfPath)

	data, engineSize, err := loadSelfData(selfPath)
	if err != nil {
		fmt.Println("読み込みエラー:", err)
		os.Exit(1)
	}

	if data == nil {
		fmt.Println("[起動] データなし。まっさらな状態で起動します。")
	} else {
		fmt.Println("[起動] 埋め込みデータを読み込みました:")
		fmt.Println("----------------------------------------")
		fmt.Println(string(data))
		fmt.Println("----------------------------------------")
	}

	switch {
	// --save "テキスト" 出力ファイル名（別名保存）
	case len(os.Args) >= 4 && os.Args[1] == "--save":
		newData := []byte(os.Args[2])
		outPath := os.Args[3]

		if err := saveSnapshot(selfPath, engineSize, newData, outPath); err != nil {
			fmt.Println(".save エラー:", err)
			os.Exit(1)
		}
		fmt.Printf("[.save] 新しい実行ファイルを生成しました: %s\n", outPath)

	// --save-self "テキスト"（自分自身を上書きして終了）
	case len(os.Args) >= 3 && os.Args[1] == "--save-self":
		newData := []byte(os.Args[2])

		if err := saveSnapshotSelf(selfPath, engineSize, newData); err != nil {
			fmt.Println(".save-self エラー:", err)
			os.Exit(1)
		}
		fmt.Printf("[.save-self] 自身を上書きしました: %s\n", selfPath)
		// ここでそのまま正常終了。プロセスは差し替え前の古いinodeで動作していたため問題なく終了できる。
	}
}
