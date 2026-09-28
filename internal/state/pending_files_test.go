package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func stagedFiles(ids ...string) []FileView {
	var out []FileView
	for _, id := range ids {
		out = append(out, FileView{ID: id, Name: id + ".png", Ext: "png", Size: 3, Mime: "image/png", Staged: true})
	}
	return out
}

func TestPendingPostShowsItsLocalFiles(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", nil, true, 5, 0)
	pd := s.AddPending("off", "", "", stagedFiles("a1", "a2")...)
	assert.Equal(t, stagedFiles("a1", "a2"), pd.Files)
	v, _ := s.ChannelView("off")
	require.Len(t, v.Posts, 1)
	assert.True(t, v.Posts[0].Pending)
	assert.Empty(t, v.Posts[0].Message, "files without text")
	assert.Equal(t, stagedFiles("a1", "a2"), v.Posts[0].Files)

	b, err := json.Marshal(v.Posts[0].Files[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"a1","name":"a1.png","ext":"png","size":3,"mime":"image/png","staged":true}`, string(b))
	b, err = json.Marshal(FileView{ID: "f1", Name: "a.pdf", Size: 1, Mime: "application/pdf"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "staged", "a server file says nothing about staging")
}

func TestDroppedPendingPostsReleaseTheirFiles(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("off", nil, true, 5, 0)
	s.SetWindow("town", nil, true, 5, 0)
	assert.Empty(t, s.TakeReleased())

	plain := s.AddPending("off", "", "no files")
	sent := s.AddPending("off", "", "sent", stagedFiles("s1", "s2")...)
	discarded := s.AddPending("off", "", "discarded", stagedFiles("d1")...)
	s.AddPending("town", "", "channel left", stagedFiles("g1")...)
	kept := s.AddPending("off", "", "still sending", stagedFiles("k1")...)
	assert.ElementsMatch(t, []string{"s1", "s2", "d1", "g1", "k1"}, s.PendingAttachments())
	assert.Empty(t, s.TakeReleased(), "nothing dropped yet")

	// Confirmed by the server (REST response or WS echo).
	p := mkPost("real", "off", "u1", 6000)
	p.PendingPostID = sent.ID
	s.ApplyEvent(postedEv(p))
	assert.Equal(t, []string{"s1", "s2"}, s.TakeReleased())
	s.PostCreated(p)
	assert.Empty(t, s.TakeReleased(), "released once")

	s.DropPending("off", discarded.ID)
	s.DropPending("off", plain.ID)
	assert.Equal(t, []string{"d1"}, s.TakeReleased())

	db, _ := json.Marshal(map[string]string{"channel_id": "town", "remover_id": "u2"})
	s.ApplyEvent(ws.Event{Type: "user_removed", Data: db, Broadcast: ws.Broadcast{UserID: "u1"}})
	assert.Equal(t, []string{"g1"}, s.TakeReleased(), "the channel is gone with its pending posts")

	assert.Equal(t, []string{"k1"}, s.PendingAttachments())
	v, _ := s.ChannelView("off")
	var ids []string
	for _, x := range v.Posts {
		ids = append(ids, x.ID)
	}
	assert.Equal(t, []string{"real", kept.ID}, ids)
}

func TestRefreshPendingProgressUpdatesStagedFilesInPlace(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", nil, true, 5, 0)
	s.AddPending("off", "", "", stagedFiles("a1", "a2")...)

	changed := s.RefreshPendingProgress("off", func(id string) (FileProgress, bool) {
		if id == "a1" {
			return FileProgress{State: "uploading", Sent: 2}, true
		}
		return FileProgress{}, false
	})
	assert.True(t, changed)
	v, _ := s.ChannelView("off")
	require.Len(t, v.Posts, 1)
	got := v.Posts[0].Files
	require.Len(t, got, 2)
	assert.Equal(t, FileView{ID: "a1", Name: "a1.png", Ext: "png", Size: 3, Mime: "image/png", Staged: true, State: "uploading", Sent: 2}, got[0])
	assert.Equal(t, FileView{ID: "a2", Name: "a2.png", Ext: "png", Size: 3, Mime: "image/png", Staged: true}, got[1], "untouched: get reported not-ok")

	// The same values again: nothing actually changed.
	changed = s.RefreshPendingProgress("off", func(id string) (FileProgress, bool) {
		if id == "a1" {
			return FileProgress{State: "uploading", Sent: 2}, true
		}
		return FileProgress{}, false
	})
	assert.False(t, changed, "no change: nothing for the caller to tell the UI")

	// A failure replaces state/sent with an error code.
	changed = s.RefreshPendingProgress("off", func(id string) (FileProgress, bool) {
		if id == "a1" {
			return FileProgress{State: "failed", Error: "unreachable"}, true
		}
		return FileProgress{}, false
	})
	assert.True(t, changed)
	v, _ = s.ChannelView("off")
	assert.Equal(t, "failed", v.Posts[0].Files[0].State)
	assert.Equal(t, "unreachable", v.Posts[0].Files[0].Error)
	assert.Zero(t, v.Posts[0].Files[0].Sent, "a fresh failure resets progress")

	// An unknown channel is a no-op, not a panic.
	assert.False(t, s.RefreshPendingProgress("nope", func(string) (FileProgress, bool) { return FileProgress{}, true }))
}

func TestOnlyAFailedPendingPostIsRetried(t *testing.T) {
	s := newFixture()
	pd := s.AddPending("off", "", "", stagedFiles("a1")...)
	_, _, ok := s.RetryPending("off", pd.ID)
	assert.False(t, ok, "still being sent: a second send would race the first")
	s.FailPending("off", pd.ID)
	again, _, ok := s.RetryPending("off", pd.ID)
	require.True(t, ok)
	assert.Equal(t, stagedFiles("a1"), again.Files)
}
