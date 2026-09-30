package mmsync

import (
	"context"
	"log/slog"

	"github.com/spk/spk-mm-client/internal/state"
)

// ReactionUsers answers "who reacted with emoji on postID": everyone the
// post's held copy carries (state.ReactorIDs — window, history or the
// thread cache), minus me (the UI adds "You"), oldest reaction first.
// Profiles state doesn't have yet are fetched in one bounded batch; a
// fetch failure never fails the call — it returns whatever is already
// known, with the rest counted Unknown (state.ReactionUsersView), same as
// every other reaction fetch this app makes. This is a read: it proceeds
// from state alone whenever nothing is missing, even needs_reauth (unlike
// the write actions in actions.go, which fail fast on a dead session via
// s.writer() in api/chat.go).
func (w *Worker) ReactionUsers(ctx context.Context, postID, emoji string) (state.ReactionUsersView, error) {
	ids, ok := w.st.ReactorIDs(postID, emoji)
	if !ok {
		return state.ReactionUsersView{}, ErrNoPost
	}
	if missing := w.st.MissingAmong(ids); len(missing) > 0 {
		// A func literal so defer actually protects the unlock (matching
		// loadUsers/refreshUsers's own lockUsers; defer unlockUsers —
		// fix round 1: this used to unlock manually after the call, which
		// would leave the lock held forever if UsersByIDs ever panicked).
		// A caller that gives up while waiting gets what state knows.
		func() {
			if !w.lockUsers(ctx) {
				return
			}
			defer w.unlockUsers()
			users, err := w.rc.UsersByIDs(ctx, missing)
			if err != nil {
				if sessionExpired(err) {
					w.signalAuth()
				}
				slog.Warn("reactor profiles unavailable", "srv", w.srv.ID, "err", err)
				return
			}
			if len(users) > 0 {
				w.st.SetUsers(users)
			}
		}()
	}
	return w.st.ResolveReactors(ids), nil
}
