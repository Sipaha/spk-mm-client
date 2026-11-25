//go:build wails && linux

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

var defaultLinuxOpener = newLinuxOpener()

// OpenURL opens target in the user's browser detached from any
// PR_SET_CHILD_SUBREAPER via a transient systemd user service, falling back
// to Wails' Browser.OpenURL when systemd-run/xdg-open is unavailable or fails.
//
// Guarantee boundaries: --no-block confirms that systemd enqueued the
// transient unit (not the exit status of xdg-open), and the Wails fallback on
// hosts without a systemd user bus does not detach from a subreaper.
func OpenURL(target string) error {
	return openWithFallback("url", target, defaultLinuxOpener.open, func(u string) error {
		return application.Get().Browser.OpenURL(u)
	})
}

// OpenFile opens path with the system handler detached from any
// PR_SET_CHILD_SUBREAPER via a transient systemd user service, falling back
// to Wails' Browser.OpenFile when systemd-run/xdg-open is unavailable or fails.
//
// Guarantee boundaries: --no-block confirms that systemd enqueued the
// transient unit (not the exit status of xdg-open), and the Wails fallback on
// hosts without a systemd user bus does not detach from a subreaper.
func OpenFile(path string) error {
	return openWithFallback("file", path, defaultLinuxOpener.open, func(p string) error {
		return application.Get().Browser.OpenFile(p)
	})
}
