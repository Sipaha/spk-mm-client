//go:build wails && !gtk3

package desktop

import "unsafe"

// nativeRaise: only the GTK build raises natively; elsewhere raiseWindow
// falls back to Wails' Show/UnMinimise/Focus.
func nativeRaise(unsafe.Pointer, func()) bool { return false }
