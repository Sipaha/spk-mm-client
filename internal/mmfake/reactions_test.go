package mmfake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func reactionNames(s *Server, postID string) []string {
	var out []string
	for _, r := range s.Reactions(postID) {
		out = append(out, r.UserID+":"+r.EmojiName)
	}
	return out
}

func TestReactionsSaveDeleteAndEvents(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")
	require.NotEmpty(t, id)
	assert.Equal(t, []string{"u-bob:+1", "u-carol:+1", "u-carol:tada", "u-bob:partyparrot"}, reactionNames(s, id))

	var r model.Reaction
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, &r))
	assert.Equal(t, "fire", r.EmojiName)
	assert.Contains(t, reactionNames(s, id), "u-alice:fire")
	evs := s.Events()
	assert.Equal(t, "reaction_added", evs[len(evs)-1].Name)
	assert.ElementsMatch(t, []string{"u-alice", "u-bob"}, evs[len(evs)-1].To)

	n := len(s.Events())
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, nil))
	assert.Len(t, s.Events(), n, "an existing reaction changes nothing")
	assert.Equal(t, 403, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-bob", "post_id": id, "emoji_name": "fire"}, nil), "only as yourself")
	assert.Equal(t, 400, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "bad name"}, nil))
	assert.Equal(t, 404, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": "nope", "emoji_name": "fire"}, nil))

	require.Equal(t, 200, a.call("DELETE", "/api/v4/users/u-alice/posts/"+id+"/reactions/fire", nil, nil))
	assert.NotContains(t, reactionNames(s, id), "u-alice:fire")
	evs = s.Events()
	assert.Equal(t, "reaction_removed", evs[len(evs)-1].Name)
	assert.Equal(t, 403, a.call("DELETE", "/api/v4/users/u-bob/posts/"+id+"/reactions/+1", nil, nil), "not someone else's")

	var list model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-offtopic/posts?page=0&per_page=60", nil, &list))
	welcome := list.Posts[id]
	require.NotNil(t, welcome.Metadata)
	assert.Len(t, welcome.Metadata.Reactions, 4)
	assert.Greater(t, welcome.UpdateAt, welcome.CreateAt, "reactions bump update_at")
}

func TestReactAsPreferenceAndFailWith(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")
	s.ReactAs("bob", id, "fire") // carol is not in Off-Topic: only members react
	assert.Contains(t, reactionNames(s, id), "u-bob:fire")
	s.UnreactAs("bob", id, "fire")
	assert.NotContains(t, reactionNames(s, id), "u-bob:fire")

	require.Equal(t, 200, a.call("PUT", "/api/v4/users/me/preferences",
		[]model.Preference{{Category: "recent_emojis", Name: "u-alice", Value: `[{"name":"+1","usageCount":1}]`}}, nil))
	assert.Equal(t, `[{"name":"+1","usageCount":1}]`, s.Preference("alice", "recent_emojis", "u-alice"))

	s.FailWith("/api/v4/reactions", 400, "app.reaction.save.save.too_many_reactions")
	var e map[string]any
	require.Equal(t, 400, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, &e))
	s.SetFailure("/api/v4/reactions", 0)
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, nil))
}

// jsonCall is like authed.call but decodes the body regardless of status —
// needed to inspect the AppError id on 4xx responses (call only decodes
// below 300).
func jsonCall(t *testing.T, a authed, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.s.URL()+path, rd)
	req.Header.Set("Authorization", "Bearer "+a.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

func TestReactionRepeatedAddReturnsOriginalCreateAt(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")

	var first model.Reaction
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, &first))
	require.NotZero(t, first.CreateAt)

	// Advance the fake clock (s.chat.lastMs) with an unrelated action between
	// the original add and the no-op re-add, so a re-add that (bug) reported
	// the current clock instead of the stored reaction's create_at would show
	// a different, later timestamp here — making the assertion discriminating.
	s.ReactAs("bob", id, "wave")

	var second model.Reaction
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, &second))
	assert.Equal(t, first.CreateAt, second.CreateAt, "a repeated add returns the existing reaction's real create_at, not a fresh timestamp")
}

func TestReactionArchivedChannelErrorIDDiffersBySaveAndDelete(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")

	s.mu.Lock()
	s.chat.channels["c-offtopic"].DeleteAt = 1
	s.mu.Unlock()

	status, saveErr := jsonCall(t, a, "POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"})
	require.Equal(t, 403, status)
	assert.Equal(t, "api.reaction.save.archived_channel.app_error", saveErr["id"])

	status, delErr := jsonCall(t, a, "DELETE", "/api/v4/users/u-alice/posts/"+id+"/reactions/+1", nil)
	require.Equal(t, 403, status)
	assert.Equal(t, "api.reaction.delete.archived_channel.app_error", delErr["id"])
}

func TestPostReactionsAndBrokenReply(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")
	var list []model.Reaction
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+id+"/reactions", nil, &list))
	assert.Len(t, list, 4)
	assert.Equal(t, 404, a.call("GET", "/api/v4/posts/nope/reactions", nil, nil))

	s.BreakReplies("/api/v4/reactions", true)
	body, _ := json.Marshal(map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"})
	req, _ := http.NewRequest("POST", s.URL()+"/api/v4/reactions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.tok)
	_, err := http.DefaultClient.Do(req)
	require.Error(t, err, "the connection is closed without a reply")
	assert.Contains(t, reactionNames(s, id), "u-alice:fire", "…after the server applied it")
	s.BreakReplies("/api/v4/reactions", false)
	assert.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, nil))
}
