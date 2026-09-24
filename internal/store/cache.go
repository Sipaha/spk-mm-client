package store

import (
	"context"
	"database/sql"
)

type CacheKey struct {
	Kind string
	Key  string
}

type CacheEntry struct {
	Kind string
	Key  string
	Data []byte
}

func (s *Store) LoadCache(ctx context.Context, serverID int64) ([]CacheEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, key, data FROM cache_entries WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CacheEntry
	for rows.Next() {
		var e CacheEntry
		if err := rows.Scan(&e.Kind, &e.Key, &e.Data); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveCache writes one flush of the write-behind snapshot atomically.
func (s *Store) SaveCache(ctx context.Context, serverID int64, put []CacheEntry, del []CacheKey) error {
	if len(put) == 0 && len(del) == 0 {
		return nil
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, k := range del {
			if _, err := tx.ExecContext(ctx, `DELETE FROM cache_entries WHERE server_id = ? AND kind = ? AND key = ?`, serverID, k.Kind, k.Key); err != nil {
				return err
			}
		}
		for _, e := range put {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO cache_entries(server_id, kind, key, data) VALUES (?, ?, ?, ?)
				 ON CONFLICT(server_id, kind, key) DO UPDATE SET data = excluded.data`,
				serverID, e.Kind, e.Key, e.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ClearCache(ctx context.Context, serverID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_entries WHERE server_id = ?`, serverID)
	return err
}
