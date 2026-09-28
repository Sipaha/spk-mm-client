package mmsync

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func savedOf(h *harness, postID string) bool {
	for _, p := range h.view("c-offtopic").Posts {
		if p.ID == postID {
			return p.Saved
		}
	}
	return false
}

func TestSetSavedRoundTrip(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()

	require.NoError(t, h.w.SetSaved(ctx, id, true))
	assert.True(t, savedOf(h, id))
	assert.Equal(t, "true", h.fake.Preference("alice", "flagged_post", id))

	require.NoError(t, h.w.SetSaved(ctx, id, false))
	assert.False(t, savedOf(h, id))
	assert.Empty(t, h.fake.Preference("alice", "flagged_post", id))
}

// TestSetSavedFailureIsNotRolledBackLocally: the state only changes once
// the server confirms (unlike a reaction click) — a refused/failed write
// must leave state untouched, and the error goes straight back to the
// caller (no automatic retry, like every other POST).
func TestSetSavedFailureLeavesStateUntouched(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetFailure("/api/v4/users/me/preferences", 500)
	err := h.w.SetSaved(ctx, id, true)
	require.Error(t, err)
	assert.False(t, savedOf(h, id))
	assert.Equal(t, StatusLive, h.w.Status(), "a 500 is not a dead session")
}

// TestSetSavedUnauthorizedSignalsReauth mirrors MarkUnread/Edit/Delete: a
// 401 on the write asks the worker to sign in again (w.actionErr →
// signalAuth), same as every other action in internal/mmsync/actions.go.
func TestSetSavedUnauthorizedSignalsReauth(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetFailure("/api/v4/users/me/preferences", http.StatusUnauthorized)
	err := h.w.SetSaved(ctx, id, true)
	require.Error(t, err)
	h.eventually(func() bool { return h.w.Status() == StatusNeedsReauth }, "401 should ask for a new sign-in")
}
