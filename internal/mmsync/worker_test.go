package mmsync

import (
	"context"
	"fmt"
	"strings"
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

// TestBootstrapCarriesPostOverrides: EnablePostUsernameOverride,
// EnablePostIconOverride and HasImageProxy reach state.Config.
func TestBootstrapCarriesPostOverrides(t *testing.T) {
	h := newHarness(t, mmfake.Options{ImageProxy: true})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch did not finish")
	cfg := h.w.State().Config()
	assert.True(t, cfg.PostUsernameOverride)
	assert.True(t, cfg.PostIconOverride)
	assert.True(t, cfg.ImageProxy)

	off := newHarness(t, mmfake.Options{DisablePostOverrides: true})
	off.start()
	off.live()
	off.eventually(off.allLoaded, "prefetch did not finish")
	cfg = off.w.State().Config()
	assert.False(t, cfg.PostUsernameOverride)
	assert.False(t, cfg.PostIconOverride)
	assert.False(t, cfg.ImageProxy)
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

// Spike §4.1 п.5: CRT changed without a preferences_changed event — the
// admin switched CollapsedThreads while we were connected, or while we were
// away and the snapshot was saved in the other mode. The next bootstrap
// drops the windows (replies inline vs roots only) and refetches them.
func TestBootstrapWithOtherCRTResetsWindows(t *testing.T) {
	hasReply := func(h *harness) bool {
		for _, p := range h.view("c-town").Posts {
			if p.RootID != "" {
				return true
			}
		}
		return false
	}
	caughtUp := func(h *harness, crt bool) func() bool {
		return func() bool {
			v := h.view("c-town")
			return v.CRT == crt && v.Loaded && !v.Syncing && h.allLoaded()
		}
	}

	t.Run("live", func(t *testing.T) {
		h := newHarness(t, mmfake.Options{})
		h.fake.SeedThread("c-town", "alice", 3)
		h.start()
		h.live()
		h.eventually(caughtUp(h, false), "prefetch")
		require.True(t, hasReply(h), "sanity: without CRT replies are inline")

		h.fake.SetCollapsedThreads("always_on")
		h.fake.DropConnections(true) // the next session bootstraps afresh
		h.eventually(func() bool { return caughtUp(h, true)() && !hasReply(h) }, "windows fetched without CRT were not dropped")
	})

	t.Run("snapshot", func(t *testing.T) {
		h := newHarness(t, mmfake.Options{})
		h.fake.SeedThread("c-town", "alice", 3)
		h.start()
		h.live()
		h.eventually(caughtUp(h, false), "prefetch")
		require.True(t, hasReply(h), "sanity: without CRT replies are inline")
		h.stop()
		entries, err := h.store.LoadCache(context.Background(), h.srv.ID)
		require.NoError(t, err)
		saved := false
		for _, e := range entries {
			saved = saved || (e.Kind == "posts" && e.Key == "c-town" && strings.Contains(string(e.Data), `"root_id"`))
		}
		require.True(t, saved, "sanity: the snapshot holds the town window with replies")

		h.fake.SetCollapsedThreads("always_on")
		h.start()
		h.live()
		h.eventually(func() bool { return caughtUp(h, true)() && !hasReply(h) }, "the snapshot's windows were not dropped")
	})
}

// Review, critical 1, end to end through the fake, which now sends
// post_deleted like MM 10.11 (the pre-deletion copy, delete_at 0): our own
// DeletePost and another user's deletion each lower the root's count once.
func TestReplyDeleteLowersTheRootOnce(t *testing.T) {
	for _, crt := range []bool{true, false} {
		t.Run(fmt.Sprint("crt=", crt), func(t *testing.T) {
			h := newHarness(t, mmfake.Options{CRT: crt, SeedPosts: -1})
			root := h.fake.SeedThread("c-town", "alice", 2)
			mine := h.fake.ReplyAs("c-town", root, "alice", "mine")
			h.start()
			h.live()
			h.eventually(h.allLoaded, "prefetch")
			count := func() int64 {
				for _, p := range h.view("c-town").Posts {
					if p.ID == root {
						return p.ReplyCount
					}
				}
				return -1
			}
			require.Equal(t, int64(3), count())

			require.NoError(t, h.w.Delete(context.Background(), mine.ID))
			h.eventually(func() bool { return count() == 2 }, "our own deletion did not lower the count")
			h.fake.DeleteAs(h.fake.FindPost("c-town", "Reply 1"))
			h.eventually(func() bool { return count() == 1 }, "another user's deletion did not lower the count")
			require.Never(t, func() bool { return count() != 1 }, 300*time.Millisecond, 20*time.Millisecond, "lowered twice")
		})
	}
}

// Review, minor 7: CRT switched and noticed by a background metadata
// refresh (no reconnect, so no session bootstrap): the windows are reset
// and refetched there too.
func TestMetadataRefreshWithOtherCRTResetsWindows(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.fake.SeedThread("c-town", "alice", 3)
	h.start()
	h.live()
	hasReply := func() bool {
		for _, p := range h.view("c-town").Posts {
			if p.RootID != "" {
				return true
			}
		}
		return false
	}
	h.eventually(func() bool { return h.allLoaded() && hasReply() }, "prefetch")
	h.mu.Lock()
	sessions := len(h.statuses)
	h.mu.Unlock()

	h.fake.SetCollapsedThreads("always_on")
	h.fake.AddChannel("c-new", "Brand New", "alice", "bob") // user_added → metadata refresh
	h.eventually(func() bool {
		v := h.view("c-town")
		return v.CRT && v.Loaded && !v.Syncing && h.allLoaded() && !hasReply()
	}, "the refresh under CRT did not drop and refetch the windows")
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Len(t, h.statuses, sessions, "no reconnect: it was the refresh")
}
