package engine

import (
	"context"
	"database/sql"
	"fmt"
)

// serializer and deserializer mirror the (unexported) methods
// modernc.org/sqlite's driver connection type actually implements:
// Serialize() ([]byte, error) and Deserialize([]byte) error, with no
// schema-name argument (execdb_spec.md §7).
type serializer interface {
	Serialize() ([]byte, error)
}

type deserializer interface {
	Deserialize([]byte) error
}

// serializeConn returns conn's current database state as a single
// contiguous byte slice (spec §7). It does not itself guard against a
// concurrent writer on another connection -- Serialize() is not
// transaction-aware (.claude/rules/sqlite-quirks.md); callers that need a
// consistent snapshot of the live database use DB.serializeBarrier
// instead, which wraps this in a BEGIN IMMEDIATE barrier.
func serializeConn(conn *sql.Conn) ([]byte, error) {
	var blob []byte
	err := conn.Raw(func(driverConn any) error {
		s, ok := driverConn.(serializer)
		if !ok {
			return fmt.Errorf("engine: driver connection does not support Serialize")
		}
		v, err := s.Serialize()
		if err != nil {
			return err
		}
		blob = v
		return nil
	})
	return blob, err
}

// deserializeInto replaces conn's in-memory database with blob (spec §7).
// conn must be the only connection open against its store so far, since
// modernc.org/sqlite's SQLITE_DESERIALIZE_RESIZEABLE flag makes the
// result a normal, writable database rather than a fixed-size one
// (confirmed in engine/serialize_test.go).
func deserializeInto(conn *sql.Conn, blob []byte) error {
	return conn.Raw(func(driverConn any) error {
		d, ok := driverConn.(deserializer)
		if !ok {
			return fmt.Errorf("engine: driver connection does not support Deserialize")
		}
		return d.Deserialize(blob)
	})
}

// loadBlobInto gets blob's content into dstDSN's live database. This is
// how Open/OpenSelf/Load/LoadFrom all populate engine's live database:
// Deserialize itself only affects the exact connection it runs on, and
// never propagates to any other connection -- not even one opened
// afterward on the same DSN (.claude/rules/sqlite-quirks.md) -- because it
// reopens its target schema as an anonymous, unshared memdb store deep
// inside SQLite. Routing through a throwaway connection and then
// SQLite's own online Backup API (backupInto) sidesteps that: a backup
// copies pages through the normal btree/pager machinery, which every
// connection on dstDSN can see.
func loadBlobInto(blob []byte, dstDSN string) error {
	scratch, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer scratch.Close()
	conn, err := scratch.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := deserializeInto(conn, blob); err != nil {
		return err
	}
	return backupInto(conn, dstDSN)
}
