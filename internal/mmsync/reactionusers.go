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
		w.usersMu.Lock()
		users, err := w.rc.UsersByIDs(ctx, missing)
		w.usersMu.Unlock()
		if err != nil {
			if sessionExpired(err) {
				w.signalAuth()
			}
			slog.Warn("reactor profiles unavailable", "srv", w.srv.ID, "err", err)
		} else if len(users) > 0 {
			w.st.SetUsers(users)
		}
	}
	return w.st.ResolveReactors(ids), nil
}
