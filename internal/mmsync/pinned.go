package mmsync

import (
	"context"
	"errors"

	"github.com/spk/spk-mm-client/internal/state"
)

var ErrUnknownChannel = errors.New("mmsync: unknown channel")

func (w *Worker) PinnedPosts(ctx context.Context, channelID string) ([]state.PostView, error) {
	if _, ok := w.st.ChannelView(channelID); !ok {
		return nil, ErrUnknownChannel
	}
	if w.Status() != StatusLive {
		return nil, ErrOffline
	}
	ctx, cancel := w.withLife(ctx)
	defer cancel()
	raw, err := w.rc.PinnedPosts(ctx, channelID)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		return nil, err
	}
	posts := raw.Ascending()
	ids := make([]string, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.UserID)
	}
	w.loadProfiles(ctx, ids)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return w.st.PostViews(posts), nil
}
