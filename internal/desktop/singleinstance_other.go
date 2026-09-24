//go:build wails && !linux

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// singleInstanceOptions is a straight pass-through on non-Linux platforms.
// Windows (pkg/application/single_instance_windows.go, a named mutex + Win32
// message window) and macOS (single_instance_darwin.go,
// NSDistributedNotificationCenter) implement SingleInstance without dialing
// D-Bus, so they don't share the startup-crash risk singleinstance_linux.go
// guards against; see that file for the full story.
func singleInstanceOptions(opts application.SingleInstanceOptions) *application.SingleInstanceOptions {
	return &opts
}
