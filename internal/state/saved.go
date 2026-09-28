package state

// flaggedPostCategory mirrors the webapp's Preferences.CATEGORY_FLAGGED_POST:
// "save a post" is a flagged_post preference named by the post's id, value
// "true"; unsaving deletes it.
const flaggedPostCategory = "flagged_post"

// SetPostSaved records a save/unsave the server already confirmed (unlike
// ReactLocalWas/SetMyReaction, there is no optimistic guess to roll back
// here — the worker calls this only after SavePreferences/DeletePreferences
// succeeds, or when a preferences_changed/preferences_deleted WS event
// echoes it). Marks the post's channel changed so the feed repaints, like a
// reaction does — a post not currently held in memory changes nothing to
// repaint.
func (s *Server) SetPostSaved(postID string, saved bool) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setSavedLocked(postID, saved)
	ch, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}
	}
	return Change{Channels: []string{ch}}
}

func (s *Server) setSavedLocked(postID string, saved bool) {
	k := prefKey{flaggedPostCategory, postID}
	if saved {
		s.prefs[k] = "true"
	} else {
		delete(s.prefs, k)
	}
	s.dirty.meta = true
}
