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
	t.Setenv("SPK_MATTERMOST_HOME", dir)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, dir, p.DataDir)
	assert.Equal(t, filepath.Join(dir, "db.sqlite"), p.DBFile)
	assert.Equal(t, filepath.Join(dir, "media"), p.MediaDir)
}

func TestResolveDefaultsUnderHomeDotSpk(t *testing.T) {
	t.Setenv("SPK_MATTERMOST_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".spk", "spk-mattermost"), p.DataDir)
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
