//go:build wails && !gtk3

package desktop

import "unsafe"

// observeNatively: native drop/paste observers exist for GTK only; without
// them every drop is refused (clipboard pasting is unsupported there).
func observeNatively(unsafe.Pointer) bool { return false }
