package desktop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeDropTarget struct{ got []string }

func (f *fakeDropTarget) AttachDropped(_ context.Context, _ int64, channelID string, paths []string) int {
	for _, p := range paths {
		f.got = append(f.got, channelID+":"+p)
	}
	return len(paths)
}

func TestDropOnAChannelAttachesItsPaths(t *testing.T) {
	f := &fakeDropTarget{}
	attrs := map[string]string{"data-file-drop-target": "", "data-srv": "3", "data-channel": "c-town", "class": "x"}
	filesDropped(f, attrs, []string{"/home/u/a.txt", "/home/u/имя с пробелом.png"})
	assert.Equal(t, []string{"c-town:/home/u/a.txt", "c-town:/home/u/имя с пробелом.png"}, f.got)
}

func TestDropWithoutAChannelTargetIsIgnored(t *testing.T) {
	for _, attrs := range []map[string]string{
		nil,
		{"data-channel": "c-town"},
		{"data-srv": "x", "data-channel": "c-town"},
		{"data-srv": "3"},
		{"data-srv": "3", "data-channel": ""},
	} {
		f := &fakeDropTarget{}
		filesDropped(f, attrs, []string{"/a"})
		assert.Empty(t, f.got, "%v", attrs)
	}
	f := &fakeDropTarget{}
	filesDropped(f, map[string]string{"data-srv": "3", "data-channel": "c"}, nil)
	assert.Empty(t, f.got, "no files")
}
