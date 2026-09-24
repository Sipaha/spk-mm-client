package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenCreatesOwnerOnlyFileAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer st.Close()
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
	var v int
	require.NoError(t, st.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v))
	assert.Equal(t, 2, v)
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, st.Close())
	st, err = Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, st.Close())
}

func TestWithTxRollsBackOnError(t *testing.T) {
	st := openTest(t)
	boom := errors.New("boom")
	err := st.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO servers(url, site_url, name, created_at) VALUES('https://a', '', 'a', 1)`)
		require.NoError(t, err)
		return boom
	})
	assert.ErrorIs(t, err, boom)
	list, err := st.ListServers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, list)
}
