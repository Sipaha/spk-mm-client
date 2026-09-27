//go:build wails && !gtk3

package desktop

// cancelFileDialogs: only the GTK dialog needs a hand to close on quit.
func cancelFileDialogs() {}
