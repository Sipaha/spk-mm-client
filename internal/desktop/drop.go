package desktop

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/api"
)

// dropTarget takes files dropped onto a channel or thread panel composer
// (api.Service). rootID: "" the channel's own composer, else a thread's
// reply composer (data-root of the drop target).
type dropTarget interface {
	AttachDropped(ctx context.Context, id int64, channelID, rootID string, paths []string) int
	DropRefused(id int64, channelID, rootID, code string)
}

const (
	// dropWindow: a dropped path counts this long after the native drop
	// (the JS round trip of Wails takes milliseconds).
	dropWindow = 5 * time.Second
	// maxDropPaths bounds one drop; the rest is refused (too_many).
	maxDropPaths = 100
	// maxDropRecord bounds the paths remembered from native drops.
	maxDropRecord = 1000
)

// dropGate remembers the paths native GTK drops on the webview carried
// (recorded by the observer in observe_gtk.go, before Wails sends them
// through the page) and admits a WindowFilesDropped path only if a native
// drop carried it within dropWindow — once. Page script can call Wails'
// FilesDropped with any path; such a forged drop is refused.
type dropGate struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

func newDropGate() *dropGate { return &dropGate{seen: map[string]time.Time{}, now: time.Now} }

// record notes the paths of a native drop.
func (g *dropGate) record(paths []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for p, at := range g.seen {
		if now.Sub(at) > dropWindow {
			delete(g.seen, p)
		}
	}
	for _, p := range paths {
		if len(g.seen) >= maxDropRecord {
			break
		}
		g.seen[p] = now
	}
}

// admit splits paths into those a recent native drop carried (used up
// now) and the rest.
func (g *dropGate) admit(paths []string) (ok, refused []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, p := range paths {
		if at, seen := g.seen[p]; seen && now.Sub(at) <= dropWindow {
			delete(g.seen, p)
			ok = append(ok, p)
			continue
		}
		refused = append(refused, p)
	}
	return ok, refused
}

// filesDropped handles Wails' WindowFilesDropped: attrs are the attributes
// of the page element under the drop that carries data-file-drop-target
// (drops elsewhere never get here); its data-srv and data-channel say
// which channel's next message the files go to, and data-root (absent:
// "") the thread panel's composer instead of the channel's. The paths
// pass through the page on their way (Wails' runtime), so only those the
// native drop carried (gate) are taken; the service then refuses the
// app's own data.
func filesDropped(t dropTarget, gate *dropGate, attrs map[string]string, paths []string) {
	srv, err := strconv.ParseInt(attrs["data-srv"], 10, 64)
	ch := attrs["data-channel"]
	root := attrs["data-root"]
	if err != nil || ch == "" || len(paths) == 0 {
		slog.Warn("file drop ignored: no channel target", "files", len(paths))
		return
	}
	if len(paths) > maxDropPaths {
		t.DropRefused(srv, ch, root, api.CodeTooMany)
		paths = paths[:maxDropPaths]
	}
	ok, refused := gate.admit(paths)
	if len(refused) > 0 {
		slog.Warn("dropped paths not carried by a native drop refused", "count", len(refused))
		t.DropRefused(srv, ch, root, api.CodeNotDropped)
	}
	if len(ok) > 0 {
		t.AttachDropped(context.Background(), srv, ch, root, ok)
	}
}
