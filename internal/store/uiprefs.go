package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetUIPref returns a stored app-wide UI preference, "" if it was never set.
func (s *Store) GetUIPref(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM ui_prefs WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetUIPref upserts one app-wide UI preference.
func (s *Store) SetUIPref(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ui_prefs(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}
