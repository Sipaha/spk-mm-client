// Package paths resolves where spk-mattermost keeps its data.
package paths

import (
	"os"
	"path/filepath"
)

const appName = "spk-mattermost"

type Paths struct {
	DataDir  string
	DBFile   string
	MediaDir string
}

// Resolve returns ~/.spk/spk-mattermost (house convention shared with
// spk-mail/spk-cockpit), overridable via SPK_MATTERMOST_HOME for tests and
// e2e runs.
func Resolve() (Paths, error) {
	dir := os.Getenv("SPK_MATTERMOST_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".spk", appName)
	}
	return Paths{
		DataDir:  dir,
		DBFile:   filepath.Join(dir, "db.sqlite"),
		MediaDir: filepath.Join(dir, "media"),
	}, nil
}

// Ensure creates DataDir owner-only. The dir holds session tokens in the DB.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(p.DataDir, 0o700)
}
