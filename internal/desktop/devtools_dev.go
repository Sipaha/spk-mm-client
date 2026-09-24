//go:build wails && !production

package desktop

// Dev builds keep DevTools and the "test notification" tray item.
const (
	devToolsEnabled = true
	devMenu         = true
)
