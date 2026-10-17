package store

import "context"

// Health exercises a real read through SQLite without writing application data.
func (s *Store) Health(ctx context.Context) error {
	var value int
	return s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM messages LIMIT 1)").Scan(&value)
}
