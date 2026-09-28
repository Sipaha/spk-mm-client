package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (f *chatFixture) postSaved(id int64, ch, postID string) bool {
	v, err := f.svc.GetChannel(context.Background(), id, ch)
	require.NoError(f.t, err)
	for _, p := range v.Posts {
		if p.ID == postID {
			return p.Saved
		}
	}
	f.t.Fatalf("post %s not in %s", postID, ch)
	return false
}

func TestSetPostSavedRoundTrip(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" && f.loaded(id, "c-offtopic") }, "never live")
	postID := fake.FindPost("c-offtopic", "Welcome to off-topic")
	require.NotEmpty(t, postID)
	ctx := context.Background()

	require.NoError(t, f.svc.SetPostSaved(ctx, id, postID, true))
	assert.True(t, f.postSaved(id, "c-offtopic", postID))
	assert.Equal(t, "true", fake.Preference("alice", "flagged_post", postID))

	require.NoError(t, f.svc.SetPostSaved(ctx, id, postID, false))
	assert.False(t, f.postSaved(id, "c-offtopic", postID))
	assert.Empty(t, fake.Preference("alice", "flagged_post", postID))
}

// TestSetPostSavedSessionExpired mirrors TestAttachmentUnauthorizedAsksForSignIn:
// a 401 on the write fails with CodeSessionExpired and moves the server to
// needs_reauth; once there, s.writer() fails the next call fast, no network.
func TestSetPostSavedSessionExpired(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" && f.loaded(id, "c-offtopic") }, "never live")
	postID := fake.FindPost("c-offtopic", "Welcome to off-topic")
	fake.SetFailure("/api/v4/users/me/preferences", http.StatusUnauthorized)

	assert.Equal(t, CodeSessionExpired, codeOf(f.svc.SetPostSaved(context.Background(), id, postID, true)))
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "the 401 did not end the session")
	assert.Equal(t, CodeSessionExpired, codeOf(f.svc.SetPostSaved(context.Background(), id, postID, true)), "fails fast, no network")
}
