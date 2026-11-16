package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/spk/spk-mm-client/internal/store"
)

func channelPostIDs(v ChannelView) []string {
	out := make([]string, len(v.Posts))
	for i, p := range v.Posts {
		out[i] = p.ID
	}
	return out
}

func TestPendingReconciledAcrossRESTSegmentAndThreadReads(t *testing.T) {
	t.Run("SetWindow reconciles own pending root and CRT reply and ignores foreign PendingPostID", func(t *testing.T) {
		s := crtFixture(true)
		ownRootPend := s.AddPending("town", "", "my root", stagedFiles("f-root")...)
		ownReplyPend := s.AddPending("town", "r0", "my reply", stagedFiles("f-reply")...)
		keptPend := s.AddPending("town", "", "still sending", stagedFiles("f-kept")...)

		// A foreign user echoing keptPend.ID must never drop our unsent post.
		spoof := mkPost("p-foreign", "town", "u2", 1500)
		spoof.PendingPostID = keptPend.ID

		confirmedRoot := mkPost("p-own-root", "town", "u1", 2000)
		confirmedRoot.PendingPostID = ownRootPend.ID
		confirmedReply := reply("p-own-reply", "r0", "u1", 2100, 1)
		confirmedReply.PendingPostID = ownReplyPend.ID

		_, gen := s.FetchMode()
		s.SetWindow("town", []model.Post{spoof, confirmedRoot, confirmedReply, confirmedRoot}, true, 3000, gen)

		assert.ElementsMatch(t, []string{"f-root", "f-reply"}, s.TakeReleased())
		assert.Equal(t, []string{"f-kept"}, s.PendingAttachments())

		v, ok := s.ChannelView("town")
		require.True(t, ok)
		assert.Equal(t, []string{"p-foreign", "p-own-root", keptPend.ID}, channelPostIDs(v))
	})

	t.Run("MergeSince reconciles failed pending after broken reply and WS drop", func(t *testing.T) {
		s := newFixture()
		s.ClearGuard()
		_, gen := s.FetchMode()
		s.SetWindow("off", []model.Post{mkPost("p0", "off", "u2", 1000)}, true, 1000, gen)

		failedPend := s.AddPending("off", "", "sent before drop", stagedFiles("f-1")...)
		s.FailPending("off", failedPend.ID)
		deletedPend := s.AddPending("off", "", "sent then deleted", stagedFiles("f-del")...)

		confirmed := mkPost("p-confirmed", "off", "u1", 2000)
		confirmed.PendingPostID = failedPend.ID
		deleted := mkPost("p-deleted", "off", "u1", 2100)
		deleted.PendingPostID = deletedPend.ID
		deleted.DeleteAt = 2200

		s.MarkStale(1500)
		s.MergeSince("off", []model.Post{confirmed, deleted}, 2500, gen)

		assert.ElementsMatch(t, []string{"f-1", "f-del"}, s.TakeReleased())
		v, ok := s.ChannelView("off")
		require.True(t, ok)
		assert.Equal(t, []string{"p0", "p-confirmed"}, channelPostIDs(v))
		assert.False(t, v.Posts[1].Pending)
		assert.False(t, v.Posts[1].Failed)
	})

	t.Run("SetSegment AppendNewer AppendOlder and ApplyRevalidation reconcile pending", func(t *testing.T) {
		s := newFixture()
		s.ClearGuard()
		s.SetActive("off")
		_, gen := s.FetchMode()
		s.SetWindow("off", []model.Post{mkPost("w1", "off", "u2", 9000)}, false, 9000, gen)

		pSeg := s.AddPending("off", "", "in segment")
		pNewer := s.AddPending("off", "", "in newer")
		pOlder := s.AddPending("off", "", "in older")
		pReval := s.AddPending("off", "", "in reval")

		target := mkPost("s2", "off", "u1", 3000)
		target.PendingPostID = pSeg.ID
		op, ok := s.BeginJump("off")
		require.True(t, ok)
		s.MarkStale(9500) // reconnect while jump pages were in flight marks segment stale for revalidation
		before := model.PostList{
			Order:      []string{"s1"},
			Posts:      map[string]model.Post{"s1": mkPost("s1", "off", "u2", 2000)},
			PrevPostID: "s0",
		}
		after := model.PostList{
			Order:      []string{"s3"},
			Posts:      map[string]model.Post{"s3": mkPost("s3", "off", "u2", 4000)},
			NextPostID: "s4",
		}
		require.True(t, s.SetSegment(op, target, before, after))

		newerOp, ok := s.BeginLoadNewer("off")
		require.True(t, ok)
		cNewer := mkPost("s4", "off", "u1", 5000)
		cNewer.PendingPostID = pNewer.ID
		s.AppendNewer(newerOp, model.PostList{
			Order:      []string{"s4"},
			Posts:      map[string]model.Post{"s4": cNewer},
			NextPostID: "w1",
		})

		olderOp, ok := s.BeginLoadOlder("off")
		require.True(t, ok)
		cOlder := mkPost("s0", "off", "u1", 1000)
		cOlder.PendingPostID = pOlder.ID
		s.AppendOlder(olderOp, model.PostList{
			Order: []string{"s0"},
			Posts: map[string]model.Post{"s0": cOlder},
		})

		revalOp, ok := s.BeginRevalidate("off")
		require.True(t, ok)
		cReval := mkPost("s2b", "off", "u1", 3500)
		cReval.PendingPostID = pReval.ID
		high := cNewer
		require.True(t, s.ApplyRevalidation(revalOp, Reread{
			High:    &high,
			Pages:   []model.PostList{{Order: []string{"s3", "s2b", "s2", "s1", "s0"}, Posts: map[string]model.Post{"s3": after.Posts["s3"], "s2b": cReval, "s2": target, "s1": before.Posts["s1"], "s0": cOlder}}},
			Covered: true,
		}))

		v, ok := s.ChannelView("off")
		require.True(t, ok)
		assert.Equal(t, []string{"s0", "s1", "s2", "s2b", "s3", "s4", "w1"}, channelPostIDs(v))
	})

	t.Run("SetThreadPage AppendOlderReplies and SetThreadFocus reconcile pending and dedupe replies", func(t *testing.T) {
		s := crtFixture(true)
		root := mkPost("R", "town", "u2", 1000)
		pPage := s.AddPending("town", "R", "page reply")
		pOlder := s.AddPending("town", "R", "older reply")

		epoch, _, ok := s.OpenThread("town", "R")
		require.True(t, ok)
		r2 := reply("r2", "R", "u1", 3000, 2)
		r2.PendingPostID = pPage.ID
		more := true
		s.SetThreadPage("R", epoch, model.PostList{
			Order:   []string{"R", "r2", "r2"},
			Posts:   map[string]model.Post{"R": root, "r2": r2},
			HasNext: &more,
		})
		r1 := reply("r1", "R", "u1", 2000, 1)
		r1.PendingPostID = pOlder.ID
		noMore := false
		s.AppendOlderReplies("R", epoch, "r2", model.PostList{
			Order:   []string{"r1", "r1"},
			Posts:   map[string]model.Post{"r1": r1},
			HasNext: &noMore,
		})
		tv := mustThread(t, s, "R")
		assert.Equal(t, []string{"R", "r1", "r2"}, threadPostIDs(tv))

		pFocus := s.AddPending("town", "R2", "focused reply")
		root2 := mkPost("R2", "town", "u2", 1000)
		fOp, held, ok := s.FocusThread("town", "R2", "rf")
		require.True(t, ok)
		require.False(t, held)
		rf := reply("rf", "R2", "u1", 2000, 1)
		rf.PendingPostID = pFocus.ID
		require.True(t, s.SetThreadFocus(fOp, rf, model.PostList{Posts: map[string]model.Post{"R2": root2}}, model.PostList{}))
		tv2 := mustThread(t, s, "R2")
		assert.Equal(t, []string{"R2", "rf"}, threadPostIDs(tv2))
	})
}

func TestAddPendingMonotonicAcrossConfirmationsAndClockSkew(t *testing.T) {
	nowMs := int64(5_000_000)
	s := New(func() time.Time { return time.UnixMilli(nowMs) })
	s.Bootstrap(fixture())
	s.ClearGuard()
	_, gen := s.FetchMode()
	s.SetWindow("off", nil, true, 0, gen)

	p1 := s.AddPending("off", "", "first")
	confirmed := mkPost("real-1", "off", "u1", nowMs)
	confirmed.PendingPostID = p1.ID
	s.PostCreated(confirmed)

	// Same ms after p1 was dropped from s.pending, then clock skew backwards.
	p2 := s.AddPending("off", "", "second")
	nowMs -= 500
	p3 := s.AddPending("town", "", "third")

	assert.NotEqual(t, p1.ID, p2.ID)
	assert.NotEqual(t, p2.ID, p3.ID)
	assert.NotEqual(t, p1.ID, p3.ID)

	v, ok := s.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, []string{"real-1", p2.ID}, channelPostIDs(v))
}

func TestDistinctPostsWithIdenticalContentAndTimePreservedWhileDuplicateIDsCollapse(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	_, gen := s.FetchMode()

	pTargetOld := mkPost("p-0139", "off", "u2", 1_700_000_000_000)
	pTargetOld.Message = "status check"
	pTargetNew := pTargetOld
	pTargetNew.Message = "status check (edited)"
	pTargetNew.UpdateAt = pTargetOld.CreateAt + 50
	pTargetNew.EditAt = pTargetOld.CreateAt + 50

	pTwin1 := mkPost("p-twin-1", "off", "u2", 1_700_000_000_000)
	pTwin1.Message = "ok"
	pTwin2 := mkPost("p-twin-2", "off", "u2", 1_700_000_000_000)
	pTwin2.Message = "ok"

	s.SetWindow("off", []model.Post{pTargetOld, pTwin1, pTwin2, pTargetNew}, true, 100, gen)

	patch1, ok := s.ChannelDelta("off", 0)
	require.True(t, ok)
	require.True(t, patch1.Reset)
	require.Len(t, patch1.Posts, 3)
	assert.Equal(t, []string{"p-0139", "p-twin-1", "p-twin-2"}, []string{patch1.Posts[0].ID, patch1.Posts[1].ID, patch1.Posts[2].ID})

	// Snapshot round-trip with injected duplicate post IDs in persisted window.
	put, _ := s.TakeSnapshot()
	corruptedEntries := make([]store.CacheEntry, 0, len(put))
	for _, e := range put {
		if e.Kind == kindPosts && e.Key == "off" {
			rawWin := Window{
				Posts:    []model.Post{pTargetOld, pTwin1, pTargetNew, pTwin2, pTargetOld},
				Loaded:   true,
				Complete: true,
				SyncedAt: 100,
			}
			b, err := json.Marshal(rawWin)
			require.NoError(t, err)
			e.Data = b
		}
		corruptedEntries = append(corruptedEntries, e)
	}
	s2 := New(fixedNow)
	require.NoError(t, s2.Restore(corruptedEntries))
	v2, ok := s2.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, []string{"p-0139", "p-twin-1", "p-twin-2"}, channelPostIDs(v2))
	assert.Equal(t, "status check (edited)", v2.Posts[0].Message)
}

func TestStalePageOverlapKeepsNewerLiveEditAndReactionWithoutDuplication(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	_, gen := s.FetchMode()

	initial := mkPost("p-live", "off", "u2", 1000)
	initial.Message = "v1"
	s.SetWindow("off", []model.Post{initial}, true, 1000, gen)

	// Live edit + live reaction arrive over WS with newer UpdateAt.
	edited := initial
	edited.Message = "v2 live"
	edited.EditAt = 2000
	edited.UpdateAt = 2000
	s.ApplyPostUpdate(edited)
	rb, _ := json.Marshal(model.Reaction{UserID: "u1", PostID: "p-live", EmojiName: "thumbsup", CreateAt: 2100})
	rd, _ := json.Marshal(map[string]any{"reaction": string(rb)})
	s.ApplyEvent(ws.Event{Type: "reaction_added", Data: rd, Broadcast: ws.Broadcast{ChannelID: "off"}})

	// A stale REST page read at t=1000 (carrying duplicate entries for p-live) lands afterwards.
	s.SetWindow("off", []model.Post{initial, initial}, true, 1500, gen)

	v, ok := s.ChannelView("off")
	require.True(t, ok)
	require.Len(t, v.Posts, 1)
	assert.Equal(t, "p-live", v.Posts[0].ID)
	assert.Equal(t, "v2 live", v.Posts[0].Message)
	assert.Equal(t, []ReactionView{{Emoji: "thumbsup", Count: 1, Mine: true}}, v.Posts[0].Reactions)
}
