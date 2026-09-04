package engine

import (
	"database/sql"
	"fmt"

	"modernc.org/sqlite"
)

// backupper mirrors modernc.org/sqlite's (unexported) driver connection
// type's exported NewBackup method: func (c *conn) NewBackup(dstUri
// string) (*Backup, error). Reached via sql.Conn.Raw() + a local
// interface assertion, the same technique serializer/deserializer in
// serialize.go use. Naming the return type requires a non-blank import of
// modernc.org/sqlite (engine.go's blank import stays for driver
// registration; this one exists purely to spell *sqlite.Backup).
type backupper interface {
	NewBackup(dstUri string) (*sqlite.Backup, error)
}

// backupInto copies conn's entire current database content into dstDSN's
// live database via SQLite's online Backup API, replacing whatever
// dstDSN held before (spec §4: Load replaces, never merges -- a full
// page-for-page copy leaves nothing of the destination's prior content
// behind). Unlike Deserialize, a backup propagates through SQLite's
// normal btree/pager machinery, so the result becomes visible to every
// connection on dstDSN, including ones opened before the backup ran (see
// .claude/rules/sqlite-quirks.md for why Deserialize itself cannot do
// this).
//
// dstDSN's database must already have at least one connection open and
// held alive by the caller (DB.keeper does this for engine's own live
// database): Backup.Finish closes its own destination connection, and a
// memdb store with no connections left open can be freed as soon as that
// happens.
func backupInto(conn *sql.Conn, dstDSN string) error {
	return conn.Raw(func(driverConn any) error {
		b, ok := driverConn.(backupper)
		if !ok {
			return fmt.Errorf("engine: driver connection does not support NewBackup")
		}
		bk, err := b.NewBackup(dstDSN)
		if err != nil {
			return err
		}
		more, err := bk.Step(-1)
		if err != nil {
			return err
		}
		if more {
			return fmt.Errorf("engine: backup reported more pages remaining after Step(-1)")
		}
		return bk.Finish()
	})
}
