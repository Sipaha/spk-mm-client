package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHonoursEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SPK_MM_CLIENT_HOME", dir)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, dir, p.DataDir)
	assert.Equal(t, filepath.Join(dir, "db.sqlite"), p.DBFile)
	assert.Equal(t, filepath.Join(dir, "media"), p.MediaDir)
	assert.Equal(t, filepath.Join(dir, "tmp"), p.TmpDir)
}

func TestResolveDefaultsUnderHomeDotSpk(t *testing.T) {
	t.Setenv("SPK_MM_CLIENT_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".spk", "mm-client"), p.DataDir)
}

func TestResolveMigratesOldDataDir(t *testing.T) {
	t.Setenv("SPK_MM_CLIENT_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldDir := filepath.Join(home, ".spk", "spk-mattermost")
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "db.sqlite"), []byte("x"), 0o600))

	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".spk", "mm-client"), p.DataDir)

	_, statErr := os.Stat(oldDir)
	assert.True(t, os.IsNotExist(statErr))
	data, err := os.ReadFile(p.DBFile)
	require.NoError(t, err)
	assert.Equal(t, "x", string(data))
}

func TestMigrateMovesOldDirToNew(t *testing.T) {
	base := t.TempDir()
	oldDir := filepath.Join(base, "old")
	newDir := filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "db.sqlite"), []byte("x"), 0o600))

	require.NoError(t, migrate(oldDir, newDir))

	_, statErr := os.Stat(oldDir)
	assert.True(t, os.IsNotExist(statErr))
	st, err := os.Stat(newDir)
	require.NoError(t, err)
	assert.True(t, st.IsDir())
	data, err := os.ReadFile(filepath.Join(newDir, "db.sqlite"))
	require.NoError(t, err)
	assert.Equal(t, "x", string(data))
}

func TestMigrateLeavesBothWhenNewAlreadyExists(t *testing.T) {
	base := t.TempDir()
	oldDir := filepath.Join(base, "old")
	newDir := filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	require.NoError(t, os.MkdirAll(newDir, 0o700))

	require.NoError(t, migrate(oldDir, newDir))

	_, errOld := os.Stat(oldDir)
	assert.NoError(t, errOld)
	_, errNew := os.Stat(newDir)
	assert.NoError(t, errNew)
}

func TestMigrateNoopWhenNeitherExists(t *testing.T) {
	base := t.TempDir()
	oldDir := filepath.Join(base, "old")
	newDir := filepath.Join(base, "new")

	require.NoError(t, migrate(oldDir, newDir))

	_, errOld := os.Stat(oldDir)
	assert.True(t, os.IsNotExist(errOld))
	_, errNew := os.Stat(newDir)
	assert.True(t, os.IsNotExist(errNew))
}

func TestMigratePermissionErrorDoesNotBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on windows")
	}
	base := t.TempDir()
	oldDir := filepath.Join(base, "old")
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	roParent := filepath.Join(base, "ro")
	require.NoError(t, os.MkdirAll(roParent, 0o500))
	t.Cleanup(func() { _ = os.Chmod(roParent, 0o700) })
	newDir := filepath.Join(roParent, "new")

	err := migrate(oldDir, newDir)
	assert.Error(t, err)

	// old dir is untouched, callers keep using the new dir despite the error
	_, errOld := os.Stat(oldDir)
	assert.NoError(t, errOld)
}

func TestEnsureCreatesOwnerOnlyDir(t *testing.T) {
	p := Paths{DataDir: filepath.Join(t.TempDir(), "a", "b")}
	require.NoError(t, p.Ensure())
	st, err := os.Stat(p.DataDir)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
	}
}
