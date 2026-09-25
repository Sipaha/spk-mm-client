// Package paths resolves where spk-mm-client keeps its data.
package paths

import (
	"log/slog"
	"os"
	"path/filepath"
)

const appName = "mm-client"

// oldAppName is the data dir name used before the spk-mattermost ->
// spk-mm-client rename (Task 0, 2026-09-25). Resolve migrates it once.
const oldAppName = "spk-mattermost"

type Paths struct {
	DataDir  string
	DBFile   string
	MediaDir string
}

// Resolve returns ~/.spk/mm-client (house convention shared with
// spk-mail/spk-cockpit), overridable via SPK_MM_CLIENT_HOME for tests and
// e2e runs. For the default location it also runs a one-time migration from
// the project's former data dir, ~/.spk/spk-mattermost.
func Resolve() (Paths, error) {
	dir := os.Getenv("SPK_MM_CLIENT_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".spk", appName)
		oldDir := filepath.Join(home, ".spk", oldAppName)
		if err := migrate(oldDir, dir); err != nil {
			slog.Warn("paths: data dir migration failed, continuing with new dir", "old", oldDir, "new", dir, "err", err)
		}
	}
	return Paths{
		DataDir:  dir,
		DBFile:   filepath.Join(dir, "db.sqlite"),
		MediaDir: filepath.Join(dir, "media"),
	}, nil
}

// migrate is a one-time move of the project's former data dir to the new
// location (Task 0, 2026-09-25 rename). Both dirs live under the same
// ~/.spk, so a single os.Rename is enough to keep this cheap and synchronous
// on the startup path. If newDir already exists both are left alone (the
// user or a previous partial migration may have put something there); if
// oldDir is missing there is nothing to do. Errors are returned so the
// caller can log them, but they never block startup — the caller always
// keeps using newDir regardless.
func migrate(oldDir, newDir string) error {
	_, oldErr := os.Stat(oldDir)
	switch {
	case oldErr == nil:
		// old dir exists, fall through
	case os.IsNotExist(oldErr):
		return nil
	default:
		return oldErr
	}

	_, newErr := os.Stat(newDir)
	switch {
	case newErr == nil:
		slog.Warn("paths: both old and new data dirs exist, leaving both alone", "old", oldDir, "new", newDir)
		return nil
	case os.IsNotExist(newErr):
		// new dir missing, proceed with the move
	default:
		return newErr
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return err
	}
	slog.Info("paths: migrated data dir", "old", oldDir, "new", newDir)
	return nil
}

// Ensure creates DataDir owner-only. The dir holds session tokens in the DB.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(p.DataDir, 0o700)
}
