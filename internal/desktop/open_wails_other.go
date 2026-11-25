//go:build wails && !linux

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// OpenURL delegates to Wails' native browser opener on non-Linux platforms.
func OpenURL(target string) error {
	return application.Get().Browser.OpenURL(target)
}

// OpenFile delegates to Wails' native file opener on non-Linux platforms.
func OpenFile(path string) error {
	return application.Get().Browser.OpenFile(path)
}
