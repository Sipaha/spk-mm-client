package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func ephemeral(id, ch, root string, at int64) model.Post {
	return model.Post{ID: id, ChannelID: ch, RootID: root, UserID: "u1", Type: model.PostTypeEphemeral, Message: "eph " + id, CreateAt: at}
}

func viewIDs(posts []PostView) []string {
	out := []string{}
	for _, p := range posts {
		out = append(out, p.ID)
	}
	return out
}

func TestEphemeralPostShowsInItsChannelByTime(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("town", []model.Post{mkPost("a", "town", "u2", 1000), mkPost("b", "town", "u2", 3000)}, true, 5, 0)
	eff := s.ApplyEvent(postEv("ephemeral_message", ephemeral("e1", "town", "", 2000)))
	assert.Equal(t, []string{"town"}, eff.Channels)
	assert.False(t, eff.Badge, "never counted as unread")
	v, _ := s.ChannelView("town")
	assert.Equal(t, []string{"a", "e1", "b"}, viewIDs(v.Posts))
	assert.True(t, v.Posts[1].Ephemeral)
	assert.True(t, v.Posts[1].System)
	info, _ := counts(s, "town")
	assert.Equal(t, int64(10), info.TotalMsgCount)
	assert.Equal(t, []string{"a", "b"}, windowIDs(s, "town"), "not in the window: never in the snapshot")

	other, _ := s.ChannelView("off")
	assert.NotContains(t, viewIDs(other.Posts), "e1")
	assert.Empty(t, s.ApplyEvent(postEv("ephemeral_message", ephemeral("e2", "nope", "", 2000))).Channels, "unknown channel: dropped")
}

func TestEphemeralReplyShowsInItsThread(t *testing.T) {
	s := crtFixture(true)
	root := mkPost("R", "town", "u2", 1000)
	openLoaded(t, s, root, []model.Post{reply("r1", "R", "u2", 1500, 1)})
	eff := s.ApplyEvent(postEv("ephemeral_message", ephemeral("e1", "town", "R", 1200)))
	assert.Equal(t, []string{"R"}, eff.Threads)
	assert.Equal(t, []string{"R", "e1", "r1"}, viewIDs(mustThread(t, s, "R").Posts))
	v, _ := s.ChannelView("town")
	assert.NotContains(t, viewIDs(v.Posts), "e1", "under CRT a reply shows in its thread only")
}

func TestEphemeralPostsAreBounded(t *testing.T) {
	s := newFixture()
	for i := range maxEphemeral + 5 {
		s.ApplyEvent(postEv("ephemeral_message", ephemeral(string(rune('A'+i%26))+string(rune('a'+i/26)), "town", "", int64(1000+i))))
	}
	s.mu.Lock()
	n := len(s.ephemeral)
	s.mu.Unlock()
	require.Equal(t, maxEphemeral, n)
	v, _ := s.ChannelView("town")
	assert.Equal(t, int64(1005), v.Posts[0].CreateAt, "the oldest went first")
}

func TestLeavingAChannelDropsItsEphemeralPosts(t *testing.T) {
	s := newFixture()
	s.ApplyEvent(postEv("ephemeral_message", ephemeral("e1", "town", "", 2000)))
	s.mu.Lock()
	s.forgetChannelLocked("town")
	n := len(s.ephemeral)
	s.mu.Unlock()
	assert.Zero(t, n)
}
