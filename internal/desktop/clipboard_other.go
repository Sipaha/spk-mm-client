//go:build wails && !gtk3

package desktop

import "github.com/spk/spk-mm-client/internal/api"

// newClipboard: the GTK clipboard reader is Linux (gtk3) only; elsewhere
// pasting files and pictures is unsupported for now.
func newClipboard() api.Clipboard { return nil }
