package desktop

import (
	"sync"
	"time"
)

// pasteWindow: a paste key counts this long (WebKit's paste event and the
// UI's AttachFromClipboard call follow within milliseconds).
const pasteWindow = 1500 * time.Millisecond

// pasteGate remembers the last paste key (Ctrl+V, Shift+Insert) pressed in
// the app's window, as seen natively by the observer in
// dropobserve_gtk.go — page script cannot fake it. AttachFromClipboard
// reads the clipboard only with a fresh one, used up by that read.
type pasteGate struct {
	mu  sync.Mutex
	at  time.Time
	now func() time.Time
}

func newPasteGate() *pasteGate { return &pasteGate{now: time.Now} }

func (g *pasteGate) press() {
	g.mu.Lock()
	g.at = g.now()
	g.mu.Unlock()
}

func (g *pasteGate) take() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	ok := !g.at.IsZero() && g.now().Sub(g.at) <= pasteWindow
	g.at = time.Time{}
	return ok
}
