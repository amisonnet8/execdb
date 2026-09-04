package engine

import (
	"context"
	"database/sql"
	"sync"
)

// Session is a dedicated connection to a DB's live database, obtained
// via DB.Session -- see that method's doc comment for what it is for. A
// Session deliberately has no Begin/BeginTx: BEGIN/COMMIT/ROLLBACK are
// plain SQL statements to it, executed like any other, which is enough
// to give them real transactional meaning on a connection that is not
// shared with anyone else. Wrapping them in *sql.Tx would create a
// second, conflicting notion of "the current transaction" that
// database/sql itself does not know about.
type Session struct {
	conn *sql.Conn

	mu     sync.Mutex
	closed bool
}

func (s *Session) Exec(query string, args ...any) (sql.Result, error) {
	return s.conn.ExecContext(context.Background(), query, args...)
}

func (s *Session) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.conn.ExecContext(ctx, query, args...)
}

func (s *Session) Query(query string, args ...any) (*sql.Rows, error) {
	return s.conn.QueryContext(context.Background(), query, args...)
}

func (s *Session) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.conn.QueryContext(ctx, query, args...)
}

func (s *Session) QueryRow(query string, args ...any) *sql.Row {
	return s.conn.QueryRowContext(context.Background(), query, args...)
}

func (s *Session) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.conn.QueryRowContext(ctx, query, args...)
}

// Close releases the Session's connection, rolling back any transaction
// left open on it (closing a *sql.Conn does this automatically). Close
// is idempotent: a second call is a no-op that returns nil.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
}
