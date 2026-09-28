package desktop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-mm-client/internal/api"
)

type fakeDropTarget struct {
	got     []string
	refused []string
}

func (f *fakeDropTarget) AttachDropped(_ context.Context, _ int64, channelID, rootID string, paths []string) int {
	for _, p := range paths {
		f.got = append(f.got, channelID+"/"+rootID+":"+p)
	}
	return len(paths)
}

func (f *fakeDropTarget) DropRefused(id int64, channelID, rootID, code string) {
	f.refused = append(f.refused, fmt.Sprintf("%d/%s/%s/%s", id, channelID, rootID, code))
}

func newTestDropGate(now *time.Time) *dropGate {
	g := newDropGate()
	g.now = func() time.Time { return *now }
	return g
}

var channelAttrs = map[string]string{"data-file-drop-target": "", "data-srv": "3", "data-channel": "c-town", "class": "x"}

func TestNativeDropOnAChannelAttachesItsPaths(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newTestDropGate(&now)
	f := &fakeDropTarget{}
	g.record([]string{"/home/u/a.txt", "/home/u/имя с пробелом.png"})
	now = now.Add(time.Second)
	filesDropped(f, g, channelAttrs, []string{"/home/u/a.txt", "/home/u/имя с пробелом.png"})
	assert.Equal(t, []string{"c-town/:/home/u/a.txt", "c-town/:/home/u/имя с пробелом.png"}, f.got)
	assert.Empty(t, f.refused)

	// Used up: the same paths "dropped" again by page script are refused.
	f = &fakeDropTarget{}
	filesDropped(f, g, channelAttrs, []string{"/home/u/a.txt"})
	assert.Empty(t, f.got)
	assert.Equal(t, []string{"3/c-town//not_dropped"}, f.refused)
}

// Task 4: a drop over the thread panel carries the panel's data-root, so
// its files go to that reply's composer, not the channel's.
func TestNativeDropOnAThreadPanelAttachesItsPaths(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newTestDropGate(&now)
	f := &fakeDropTarget{}
	threadAttrs := map[string]string{"data-file-drop-target": "", "data-srv": "3", "data-channel": "c-town", "data-root": "r1"}
	g.record([]string{"/home/u/a.txt"})
	filesDropped(f, g, threadAttrs, []string{"/home/u/a.txt"})
	assert.Equal(t, []string{"c-town/r1:/home/u/a.txt"}, f.got)
	assert.Empty(t, f.refused)

	// A forged drop (nothing native to admit it) is refused with the
	// thread's root too.
	f = &fakeDropTarget{}
	filesDropped(f, g, threadAttrs, []string{"/home/u/forged.txt"})
	assert.Empty(t, f.got)
	assert.Equal(t, []string{"3/c-town/r1/not_dropped"}, f.refused)

	// No data-root at all (the channel's own drop target): "" as always.
	f = &fakeDropTarget{}
	g.record([]string{"/home/u/b.txt"})
	filesDropped(f, g, channelAttrs, []string{"/home/u/b.txt"})
	assert.Equal(t, []string{"c-town/:/home/u/b.txt"}, f.got)
}

// Page script can call Wails' FilesDropped with any path: only paths a
// native drop carried moments ago get through.
func TestForgedDropIsRefused(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newTestDropGate(&now)
	f := &fakeDropTarget{}
	filesDropped(f, g, channelAttrs, []string{"/home/u/.ssh/id_rsa"})
	assert.Empty(t, f.got)
	assert.Equal(t, []string{"3/c-town//" + api.CodeNotDropped}, f.refused)

	// A real drop of one file does not let a forged second path through.
	f = &fakeDropTarget{}
	g.record([]string{"/home/u/a.txt"})
	filesDropped(f, g, channelAttrs, []string{"/home/u/a.txt", "/home/u/.ssh/id_rsa"})
	assert.Equal(t, []string{"c-town/:/home/u/a.txt"}, f.got)
	assert.Equal(t, []string{"3/c-town//not_dropped"}, f.refused)

	// Too late: a native drop counts for dropWindow only.
	f = &fakeDropTarget{}
	g.record([]string{"/home/u/b.txt"})
	now = now.Add(dropWindow + time.Millisecond)
	filesDropped(f, g, channelAttrs, []string{"/home/u/b.txt"})
	assert.Empty(t, f.got)
	assert.Equal(t, []string{"3/c-town//not_dropped"}, f.refused)
}

func TestHugeDropIsCapped(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newTestDropGate(&now)
	var paths []string
	for i := range maxDropPaths + 5 {
		paths = append(paths, fmt.Sprintf("/d/%03d", i))
	}
	g.record(paths)
	f := &fakeDropTarget{}
	filesDropped(f, g, channelAttrs, paths)
	assert.Len(t, f.got, maxDropPaths)
	assert.Equal(t, []string{"3/c-town//too_many"}, f.refused)
}

func TestDropGateStaysBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newTestDropGate(&now)
	for i := range 3 * maxDropRecord {
		g.record([]string{fmt.Sprintf("/x/%d", i)})
	}
	assert.LessOrEqual(t, len(g.seen), maxDropRecord)
	now = now.Add(dropWindow + time.Second)
	g.record([]string{"/y"})
	assert.Len(t, g.seen, 1, "expired entries are dropped")
}

func TestDropWithoutAChannelTargetIsIgnored(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newTestDropGate(&now)
	g.record([]string{"/a"})
	for _, attrs := range []map[string]string{
		nil,
		{"data-channel": "c-town"},
		{"data-srv": "x", "data-channel": "c-town"},
		{"data-srv": "3"},
		{"data-srv": "3", "data-channel": ""},
	} {
		f := &fakeDropTarget{}
		filesDropped(f, g, attrs, []string{"/a"})
		assert.Empty(t, f.got, "%v", attrs)
		assert.Empty(t, f.refused, "%v", attrs)
	}
	f := &fakeDropTarget{}
	filesDropped(f, g, map[string]string{"data-srv": "3", "data-channel": "c"}, nil)
	assert.Empty(t, f.got, "no files")
}

func TestPasteGateTakesARecentKeyOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newPasteGate()
	g.now = func() time.Time { return now }
	assert.False(t, g.take(), "no key yet")
	g.press()
	now = now.Add(pasteWindow - time.Millisecond)
	assert.True(t, g.take())
	assert.False(t, g.take(), "used up")
	g.press()
	now = now.Add(pasteWindow + time.Millisecond)
	assert.False(t, g.take(), "too late")
}
