package state

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/stretchr/testify/require"
)

func TestChannelDeltaMatchesFullProjection(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000)}, true, 5, 0)
	s.SetActive("off")
	var base uint64
	rows := map[string]PostView{}
	var order []string
	check := func() ChannelPatch {
		patch, ok := s.ChannelDelta("off", base)
		require.True(t, ok)
		if patch.Reset {
			rows = map[string]PostView{}
			order = nil
		}
		for _, id := range patch.Removed {
			delete(rows, id)
		}
		for _, p := range patch.Posts {
			rows[p.ID] = p
		}
		if patch.Reset || patch.Order != nil {
			order = patch.Order
		}
		got := patch.View
		got.Posts = []PostView{}
		for _, id := range order {
			got.Posts = append(got.Posts, rows[id])
		}
		want, ok := s.ChannelView("off")
		require.True(t, ok)
		require.Equal(t, want, got)
		base = patch.Revision
		return patch
	}
	require.True(t, check().Reset)
	patch := check()
	require.Empty(t, patch.Posts)
	require.Nil(t, patch.Order)
	s.SetDraft("off", "new draft")
	require.Empty(t, check().Posts)
	p := mkPost("b", "off", "u3", 2000)
	s.ApplyEvent(postedEv(p))
	require.Len(t, check().Posts, 1)
	p.Message, p.EditAt, p.UpdateAt = "edited", 3000, 3000
	p.Metadata = &model.PostMetadata{Files: []model.FileInfo{{ID: "f1", Name: "test.txt"}}, Reactions: []model.Reaction{{UserID: "u1", EmojiName: "+1"}}}
	s.ApplyEvent(postEv("post_edited", p))
	require.Len(t, check().Posts, 1)
	s.SetPresence([]model.Status{{UserID: "u3", Status: "away"}})
	require.Len(t, check().Posts, 1)
	s.SetPostSaved("b", true)
	require.Len(t, check().Posts, 1)
	s.SetUsers([]model.User{{ID: "u3", Username: "renamed", LastPictureUpdate: 123}})
	require.NotEmpty(t, check().Posts)
	s.AddPending("off", "", "pending")
	require.Len(t, check().Posts, 1)
	p.DeleteAt, p.UpdateAt = 4000, 4000
	s.ApplyEvent(postEv("post_deleted", p))
	require.Equal(t, []string{"b"}, check().Removed)
	base-- // lost response or another reader: never apply a patch to wrong rows
	require.True(t, check().Reset)
	s.SetActive("town")
	_, ok := s.ChannelDelta("town", base)
	require.True(t, ok)
	require.True(t, check().Reset, "channel changes replace the sole projection cache")
	_, ok = s.ChannelDelta("missing", base)
	require.False(t, ok)
}

func projectionHistory(n int) *Server {
	s := newFixture()
	s.active = "town"
	posts := make([]model.Post, n)
	for i := range posts {
		posts[i] = model.Post{ID: fmt.Sprintf("post-%d", i), ChannelID: "town", UserID: "u2", Message: strings.Repeat("text ", 100), CreateAt: int64(i + 1)}
	}
	s.older = posts[:n-60]
	s.chans["town"].Win = Window{Loaded: true, Posts: posts[n-60:]}
	return s
}

func TestLongHistoryDeltaWireSize(t *testing.T) {
	s := projectionHistory(50000)
	first, _ := s.ChannelDelta("town", 0)
	before, err := json.Marshal(first)
	require.NoError(t, err)
	s.older[0].Message = "edited old post"
	next, _ := s.ChannelDelta("town", first.Revision)
	wire, err := json.Marshal(next)
	require.NoError(t, err)
	require.Len(t, next.Posts, 1)
	require.Nil(t, next.Order)
	require.Less(t, len(wire), 2048)
	t.Logf("50000 held posts: reset=%d bytes, single old-post edit=%d bytes", len(before), len(wire))
}

func BenchmarkChannelDeltaAndJSON(b *testing.B) {
	for _, n := range []int{60, 1000, 10000, 50000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s := projectionHistory(n)
			patch, _ := s.ChannelDelta("town", 0)
			base := patch.Revision
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				patch, _ = s.ChannelDelta("town", base)
				base = patch.Revision
				if _, err := json.Marshal(patch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The wire optimization keeps one projected channel. Measure its retained
// cost separately from allocations/op, and verify navigation releases it.
func TestProjectionCacheHeapReleasedOnNavigation(t *testing.T) {
	s := projectionHistory(50000)
	heap := func() uint64 {
		// Two cycles discard unrelated sync.Pool victims from preceding JSON tests.
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	baseline := heap()
	func() {
		patch, ok := s.ChannelDelta("town", 0)
		require.True(t, ok)
		require.Len(t, patch.Posts, 50000)
	}()
	cached := heap()
	func() { _, ok := s.ChannelDelta("off", 0); require.True(t, ok) }()
	released := heap()
	runtime.KeepAlive(s)
	require.Greater(t, cached, baseline)
	require.Less(t, int64(released)-int64(baseline), int64(2<<20), "old projected history retained after navigation")
	t.Logf("50000 posts: projection retained delta=%.2f MiB; after navigation delta=%.2f MiB", float64(int64(cached)-int64(baseline))/(1<<20), float64(int64(released)-int64(baseline))/(1<<20))
}
