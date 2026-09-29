package desktop

// trayClickHides decides a tray click: only a window the user is looking at
// — shown, not minimized and focused — goes back to the tray. A hidden,
// minimized or merely visible window (behind others, e.g. the editor the
// user is typing in) is brought to the front instead: hiding a window the
// user cannot see would make the click look like it did nothing.
func trayClickHides(visible, active, minimized bool) bool {
	return visible && active && !minimized
}
