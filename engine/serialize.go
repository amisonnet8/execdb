package engine

import (
	"database/sql"
	"fmt"
)

// serializer and deserializer mirror the (unexported) methods
// modernc.org/sqlite's driver connection type actually implements:
// Serialize() ([]byte, error) and Deserialize([]byte) error, with no
// schema-name argument (confirmed by engine/serialize_test.go against
// v1.58.0; execdb_spec.md §7 originally assumed a "main" argument that
// does not exist).
type serializer interface {
	Serialize() ([]byte, error)
}

type deserializer interface {
	Deserialize([]byte) error
}

// serialize returns db's current state as a single contiguous byte slice
// (spec §7). It always runs on the keeper connection so the result is
// deterministic regardless of what other connections may be doing.
func (db *DB) serialize() ([]byte, error) {
	var blob []byte
	err := db.keeper.Raw(func(driverConn any) error {
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
// conn must be the only connection open against its shared-cache database
// so far, since modernc.org/sqlite's SQLITE_DESERIALIZE_RESIZEABLE flag
// makes the result a normal, writable database rather than a fixed-size
// one (confirmed in engine/serialize_test.go).
func deserializeInto(conn *sql.Conn, blob []byte) error {
	return conn.Raw(func(driverConn any) error {
		d, ok := driverConn.(deserializer)
		if !ok {
			return fmt.Errorf("engine: driver connection does not support Deserialize")
		}
		return d.Deserialize(blob)
	})
}
