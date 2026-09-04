// Command overwritehelper is a throwaway program used by
// engine/persist_test.go to exercise DB.Overwrite() end-to-end. Unlike a
// `go test` binary, a `go build -o <path>` binary is a real file that is
// not deleted on exit and does not live under a `go-build` temp
// directory, so it is actually overwritable (see looksLikeGoRunTempBinary
// in engine/persist.go).
package main

import (
	"fmt"
	"os"

	"github.com/amisonnet8/execdb/engine"
)

func main() {
	db, err := engine.OpenSelf()
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenSelf:", err)
		os.Exit(1)
	}
	defer db.Close()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: overwritehelper seed|read")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "seed":
		if _, err := db.Exec("CREATE TABLE t(a INTEGER)"); err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			os.Exit(1)
		}
		if _, err := db.Exec("INSERT INTO t VALUES (42)"); err != nil {
			fmt.Fprintln(os.Stderr, "insert:", err)
			os.Exit(1)
		}
		if err := db.Overwrite(); err != nil {
			fmt.Fprintln(os.Stderr, "overwrite:", err)
			os.Exit(1)
		}
		fmt.Println("seeded")
	case "read":
		var n int
		if err := db.QueryRow("SELECT a FROM t").Scan(&n); err != nil {
			fmt.Fprintln(os.Stderr, "scan:", err)
			os.Exit(1)
		}
		fmt.Println(n)
	default:
		fmt.Fprintln(os.Stderr, "usage: overwritehelper seed|read")
		os.Exit(2)
	}
}
