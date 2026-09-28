package state

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

// A post inside the post-bootstrap guard is already in the REST counters,
// but it is still new to us: it notifies without being counted twice.
func TestPostInsideGuardNotifiesWithoutDoubleCount(t *testing.T) {
	s := newFixture() // guard active: off.last_post_at = 300 from REST
	eff := s.ApplyEvent(postedEv(mkPost("early", "off", "u2", 300), "u1"))
	info, m := counts(s, "off")
	assert.Equal(t, int64(7), info.TotalMsgCount, "REST already counted it")
	assert.Equal(t, int64(0), m.MentionCount, "REST already counted the mention")
	require.NotNil(t, eff.Notify, "a guarded post still notifies")
	assert.Equal(t, "early", eff.Notify.Post.ID)
	eff = s.ApplyEvent(postedEv(mkPost("early", "off", "u2", 300), "u1"))
	assert.Nil(t, eff.Notify, "a replay does not notify twice")
}

// A post for a channel we do not know yet (just added, new DM) is kept and
// replayed once the metadata refresh brought the channel in.
func TestPostForUnknownChannelIsReplayedAfterRefresh(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	eff := s.ApplyEvent(postedEv(mkPost("first", "newch", "u2", 700), "u1"))
	assert.True(t, eff.NeedMeta)
	assert.Nil(t, eff.Notify)
	s.ApplyEvent(postedEv(mkPost("elsewhere", "ghost", "u2", 710), "u1"))

	b := fixture()
	b.Channels = append(b.Channels, model.Channel{ID: "newch", TeamID: "t1", Type: model.ChannelOpen, DisplayName: "New",
		Name: "new", TotalMsgCount: 1, TotalMsgCountRoot: 1, LastPostAt: 700, LastRootPostAt: 700, CreateAt: 600})
	b.Members = append(b.Members, model.ChannelMember{ChannelID: "newch", UserID: "u1", MentionCount: 1, MentionCountRoot: 1,
		NotifyProps: notify(), LastViewedAt: 600})
	s.Bootstrap(b)
	orphans := s.TakeOrphans()
	require.Len(t, orphans, 1, "only posts for channels that now exist are replayed")
	eff = s.ApplyEvent(orphans[0])
	require.NotNil(t, eff.Notify, "the first post of a new channel notifies")
	assert.Equal(t, "New", eff.Notify.ChannelName)
	info, m := counts(s, "newch")
	assert.Equal(t, int64(1), info.TotalMsgCount, "not counted twice")
	assert.Equal(t, int64(1), m.MentionCount)
	assert.Empty(t, s.TakeOrphans(), "taken once")
}

func TestOrphansAreBounded(t *testing.T) {
	s := newFixture()
	for i := 0; i < maxOrphans+10; i++ {
		s.ApplyEvent(postedEv(mkPost(fmt.Sprintf("p%d", i), "newch", "u2", int64(700+i))))
	}
	b := fixture()
	b.Channels = append(b.Channels, model.Channel{ID: "newch", TeamID: "t1", Type: model.ChannelOpen, Name: "new", CreateAt: 600})
	b.Members = append(b.Members, model.ChannelMember{ChannelID: "newch", UserID: "u1", NotifyProps: notify()})
	s.Bootstrap(b)
	orphans := s.TakeOrphans()
	require.Len(t, orphans, maxOrphans)
	assert.Equal(t, "p10", decodeID(t, orphans[0]), "the oldest are dropped first")
}

// Reloading the active channel's latest page (catch-up overflow) must drop
// the history loaded above the old window: it no longer joins the new one.
func TestSetWindowOnActiveChannelDropsLoadedHistory(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetActive("town")
	s.SetWindow("town", []model.Post{mkPost("w1", "town", "u2", 1000)}, false, 5, 0)
	s.AppendOlder("town", []model.Post{mkPost("o1", "town", "u2", 50)}, true, 0)
	v, _ := s.ChannelView("town")
	require.Len(t, v.Posts, 2)
	assert.False(t, v.HasMore)

	s.SetWindow("town", []model.Post{mkPost("w9", "town", "u2", 9000)}, false, 9, 0)
	v, _ = s.ChannelView("town")
	require.Len(t, v.Posts, 1, "older history is not shown across the hole")
	assert.Equal(t, "w9", v.Posts[0].ID)
	assert.True(t, v.HasMore, "HasMore follows the new window")
	assert.Equal(t, "w9", s.OldestPostID("town"))
}

func decodeID(t *testing.T, ev ws.Event) string {
	t.Helper()
	d, err := ws.DecodePosted(ev)
	require.NoError(t, err)
	return d.Post.ID
}

// Fix wave 3: a team whose categories could not be read keeps the previous ones.
func TestBootstrapKeepsCategoriesOfFailedTeams(t *testing.T) {
	s := newFixture()
	before := s.Sidebar("t1").Categories
	b := fixture()
	b.Categories = map[string]model.OrderedCategories{}
	b.CategoriesFailed = []string{"t1"}
	s.Bootstrap(b)
	assert.Equal(t, before, s.Sidebar("t1").Categories)

	b.CategoriesFailed = nil
	s.Bootstrap(b)
	assert.NotEqual(t, before, s.Sidebar("t1").Categories, "a successful empty read does replace them")
}

// A reloaded page that still reaches the old window's first post joins the
// loaded history without a hole: the history stays (a repeated fetch of the
// latest page must not throw away what the user scrolled up to).
func TestSetWindowOverlappingPageKeepsLoadedHistory(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetActive("town")
	s.SetWindow("town", []model.Post{mkPost("w1", "town", "u2", 1000), mkPost("w2", "town", "u2", 2000)}, false, 5, 0)
	s.AppendOlder("town", []model.Post{mkPost("o1", "town", "u2", 50)}, true, 0)

	s.SetWindow("town", []model.Post{mkPost("w1", "town", "u2", 1000), mkPost("w2", "town", "u2", 2000), mkPost("w3", "town", "u2", 3000)}, false, 9, 0)
	v, _ := s.ChannelView("town")
	var got []string
	for _, p := range v.Posts {
		got = append(got, p.ID)
	}
	assert.Equal(t, []string{"o1", "w1", "w2", "w3"}, got)
	assert.False(t, v.HasMore, "history already reached the first post")
}
