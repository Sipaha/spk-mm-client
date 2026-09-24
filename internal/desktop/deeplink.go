// Package desktop runs the Wails window, tray and OS integration. The untagged
// files (deep links, D-Bus decisions, tray labels) build without the `wails`
// tag, so their logic is testable everywhere.
package desktop

import (
	"strings"

	"github.com/spk/spk-mattermost/internal/auth"
)

// UniqueID identifies the app for Wails single-instance locking.
const UniqueID = "ru.spk.spk-mattermost"

// deepLinkFromArgs finds an mmauth:// callback among process args, tolerating
// surrounding quotes some Windows launchers keep.
func deepLinkFromArgs(args []string) (string, bool) {
	clean := make([]string, 0, len(args))
	for _, a := range args {
		clean = append(clean, strings.Trim(a, `"'`))
	}
	return auth.FindCallbackArg(clean)
}
