package mmsync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

func TestBootstrapLoadsEverything(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch did not finish")
	sb := h.w.State().Sidebar("")
	assert.Equal(t, "t-fake", sb.TeamID)
	assert.Equal(t, "c-town", sb.SelectedChannelID)
	town := h.view("c-town")
	assert.Len(t, town.Posts, 60)
	assert.True(t, town.HasMore)
	assert.Equal(t, "Message #150", town.Posts[59].Message)
	h.eventually(func() bool { return h.view("c-town").Posts[58].Author == "carol" }, "authors are resolved after the page lands")
}

// TestBootstrapCarriesFileLimits confirms the fake's MaxFileSize/
// EnableFileAttachments (config/client?format=old strings) reach
// state.Config as an int64/bool through fetchMeta.
func TestBootstrapCarriesFileLimits(t *testing.T) {
	h := newHarness(t, mmfake.Options{MaxFileSize: 12345, DisableFileAttachments: false})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch did not finish")
	assert.Equal(t, int64(12345), h.w.State().MaxFileSize())
	assert.True(t, h.w.State().FileAttachmentsEnabled())
}

func TestBootstrapCarriesFileAttachmentsDisabled(t *testing.T) {
	h := newHarness(t, mmfake.Options{DisableFileAttachments: true})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch did not finish")
	assert.False(t, h.w.State().FileAttachmentsEnabled())
}

func TestLivePostArrivesCountsAndNotifies(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.PostAs("c-offtopic", "bob", "hi @alice")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "hi @alice") }, "post did not arrive")
	assert.Equal(t, 1, h.w.State().Badge().Mentions)
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.notes, 1)
	assert.Equal(t, "Off-Topic", h.notes[0].ChannelName)
}

func TestReconnectWithoutLossResumes(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.DropConnections(false)
	h.fake.PostAs("c-offtopic", "bob", "sent while away")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "sent while away") }, "missed post not replayed")
	h.live()
	assert.False(t, h.view("c-offtopic").Syncing, "lossless resume needs no catch-up")
}

func TestReconnectWithLossCatchesUp(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.DropConnections(true)
	h.fake.PostAs("c-offtopic", "bob", "lost event")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "lost event") }, "since catch-up did not bring the post")
	h.eventually(func() bool { return !h.view("c-offtopic").Syncing }, "window stayed stale")
}

func TestColdStartFromSnapshotAndOfflineReadable(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.stop()
	h.fake.Close() // server gone
	h.start()
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "should keep retrying")
	assert.Len(t, h.view("c-town").Posts, 60, "cache is readable offline")
	assert.True(t, h.view("c-town").Syncing)
}

func TestCatchUpOverflowReloadsLatestPage(t *testing.T) {
	h := newHarness(t, mmfake.Options{SinceLimit: 5})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.stop()
	for i := 0; i < 10; i++ {
		h.fake.PostAs("c-town", "bob", fmt.Sprintf("offline %d", i))
	}
	h.start()
	h.live()
	h.eventually(func() bool { return h.hasMessage("c-town", "offline 9") && !h.view("c-town").Syncing }, "overflow must reload the page")
	v := h.view("c-town")
	assert.Len(t, v.Posts, 60)
	assert.Empty(t, v.GapAfter)
	assert.True(t, h.hasMessage("c-town", "offline 0"), "the reloaded page holds everything recent")
}

func TestRevokedSessionGoesNeedsReauth(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.RevokeAll()
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusNeedsReauth }, "should need re-auth")
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, StatusNeedsReauth, h.w.Status(), "no retry loop")
	assert.NotEmpty(t, h.view("c-town").Posts, "cache stays readable")
}

func TestSendPostOnce(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	require.NoError(t, h.w.Send("c-offtopic", "hello from test"))
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "hello from test") }, "sent post not shown")
	time.Sleep(200 * time.Millisecond) // let the WS echo land too
	n := 0
	for _, p := range h.view("c-offtopic").Posts {
		if p.Message == "hello from test" {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

func TestSendFailureThenRetry(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.FailPosts(1)
	require.NoError(t, h.w.Send("c-offtopic", "flaky"))
	var failedID string
	h.eventually(func() bool {
		for _, p := range h.view("c-offtopic").Posts {
			if p.Failed {
				failedID = p.ID
				return true
			}
		}
		return false
	}, "post should fail")
	h.w.Retry("c-offtopic", failedID)
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "flaky") }, "retry did not send")
	n := 0
	for _, p := range h.fake.VisiblePosts("c-offtopic") {
		if p.Message == "flaky" {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

func TestOpenChannelMarksReadWhenFocused(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.PostAs("c-offtopic", "bob", "unread one")
	h.eventually(func() bool { return h.w.State().Badge().Unread }, "should be unread")
	h.w.OpenChannel("c-offtopic") // not focused: stays unread
	time.Sleep(100 * time.Millisecond)
	assert.True(t, h.w.State().Badge().Unread)
	h.w.SetFocused(true)
	h.eventually(func() bool { return !h.w.State().Badge().Unread }, "focus should mark read")
	h.eventually(func() bool {
		return h.fake.Member("c-offtopic", "alice").MsgCount == h.fake.Channel("c-offtopic").TotalMsgCount
	}, "server not told")
	h.fake.PostAs("c-offtopic", "bob", "while open")
	h.eventually(func() bool {
		return h.fake.Member("c-offtopic", "alice").MsgCount == h.fake.Channel("c-offtopic").TotalMsgCount && h.hasMessage("c-offtopic", "while open")
	}, "open focused channel keeps being read")
}

func TestMarkUnreadStaysUnreadWhileOpen(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.SetFocused(true)
	v, _ := h.w.OpenChannel("c-town")
	require.NoError(t, h.w.MarkUnread(context.Background(), v.Posts[50].ID))
	assert.True(t, h.w.State().Badge().Unread)
	time.Sleep(200 * time.Millisecond)
	assert.True(t, h.w.State().Badge().Unread, "no auto-read after an explicit mark-unread")
}

func TestLoadOlderUntilTheStart(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.OpenChannel("c-town")
	require.NoError(t, h.w.LoadOlder(context.Background(), "c-town"))
	assert.Len(t, h.view("c-town").Posts, 120)
	require.NoError(t, h.w.LoadOlder(context.Background(), "c-town"))
	v := h.view("c-town")
	assert.Len(t, v.Posts, 150)
	assert.False(t, v.HasMore)
	assert.Equal(t, "Message #1", v.Posts[0].Message)
}

func TestEditAndDelete(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	require.NoError(t, h.w.Send("c-offtopic", "v1"))
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "v1") }, "send")
	var id string
	for _, p := range h.view("c-offtopic").Posts {
		if p.Message == "v1" {
			id = p.ID
		}
	}
	require.NoError(t, h.w.Edit(context.Background(), id, "v2"))
	assert.True(t, h.hasMessage("c-offtopic", "v2"))
	require.NoError(t, h.w.Delete(context.Background(), id))
	assert.False(t, h.hasMessage("c-offtopic", "v2"))
}

func TestAddedToChannelRefreshesSidebar(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.fake.AddChannel("c-new", "Brand New", "alice", "bob")
	h.eventually(func() bool {
		for _, c := range h.w.State().Sidebar("").Categories {
			for _, it := range c.Channels {
				if it.ID == "c-new" {
					return true
				}
			}
		}
		return false
	}, "new channel not in sidebar")
	h.eventually(func() bool { return h.view("c-new").Loaded }, "new channel not prefetched")
}

func TestSleptDetection(t *testing.T) {
	assert.False(t, slept(5*time.Second, 5*time.Second, 15*time.Second))
	assert.True(t, slept(10*time.Minute, 5*time.Second, 15*time.Second))
}
