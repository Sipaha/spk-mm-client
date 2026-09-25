package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/state"
)

var ErrEmptyMessage = errors.New("mmsync: empty message")

// OpenChannel makes the channel current: moves it to the front of the fetch
// queue and marks it read if the window is focused.
func (w *Worker) OpenChannel(channelID string) (state.ChannelView, bool) {
	w.st.SetActive(channelID)
	if it, ok := w.st.SyncItemFor(channelID); ok {
		w.queue.push(it)
	}
	if w.st.Focused() {
		w.view(channelID)
	}
	w.requestStatuses() // the open channel's authors
	w.changed(state.Change{Sidebar: true})
	return w.st.ChannelView(channelID)
}

func (w *Worker) SetFocused(f bool) {
	w.st.SetFocused(f)
	if a := w.st.Active(); f && a != "" {
		w.view(a)
	}
}

// view marks a channel read on the server, at most one request per channel
// at a time; it re-checks afterwards in case posts arrived meanwhile.
func (w *Worker) view(channelID string) {
	if !w.st.NeedsView(channelID) {
		return
	}
	if _, busy := w.viewing.LoadOrStore(channelID, true); busy {
		return
	}
	started := w.goBG(func(ctx context.Context) {
		defer w.viewing.Delete(channelID)
		for i := 0; i < 3 && w.st.NeedsView(channelID) && w.st.Active() == channelID && w.st.Focused(); i++ {
			if err := w.rc.ViewChannel(ctx, channelID); err != nil {
				if sessionExpired(err) {
					w.signalAuth()
				}
				slog.Warn("mark read failed", "srv", w.srv.ID, "channel", channelID, "err", err)
				return
			}
			w.changed(w.st.ViewedLocally(channelID, w.cfg.Now().UnixMilli()))
		}
	})
	if !started { // stopping: the read state is not sent
		w.viewing.Delete(channelID)
	}
}

func (w *Worker) LoadOlder(ctx context.Context, channelID string) error {
	before := w.st.OldestPostID(channelID)
	if before == "" {
		return nil
	}
	l, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, Before: before, CollapsedThreads: w.st.CRT()})
	if err != nil {
		return w.actionErr(err)
	}
	w.st.AppendOlder(channelID, l.Ascending(), l.PrevPostID == "")
	w.loadUsers(ctx)
	w.changed(state.Change{Channels: []string{channelID}})
	return nil
}

func (w *Worker) Send(channelID, message string) error {
	if strings.TrimSpace(message) == "" {
		return ErrEmptyMessage
	}
	p := w.st.AddPending(channelID, "", message)
	w.changed(state.Change{Channels: []string{channelID}})
	w.startCreate(p)
	return nil
}

func (w *Worker) create(ctx context.Context, p state.Pending) {
	cctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	post, err := w.rc.CreatePost(cctx, model.Post{ChannelID: p.ChannelID, RootID: p.RootID, Message: p.Message,
		PendingPostID: p.ID, UserID: w.st.Me().ID})
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("send failed", "srv", w.srv.ID, "channel", p.ChannelID, "err", err)
		w.changed(w.st.FailPending(p.ChannelID, p.ID))
		return
	}
	w.changed(w.st.PostCreated(post))
}

// Retry resends a failed post with the same pending id: if the first
// attempt did reach the server, it returns the existing post (no duplicate).
func (w *Worker) Retry(channelID, pendingID string) {
	p, ok := w.st.RetryPending(channelID, pendingID)
	if !ok {
		return
	}
	w.changed(state.Change{Channels: []string{channelID}})
	w.startCreate(p)
}

// startCreate sends p in the background; if the worker is stopping, p is
// marked failed at once instead of hanging as pending.
func (w *Worker) startCreate(p state.Pending) {
	if !w.goBG(func(ctx context.Context) { w.create(ctx, p) }) {
		w.changed(w.st.FailPending(p.ChannelID, p.ID))
	}
}

func (w *Worker) Discard(channelID, pendingID string) {
	w.changed(w.st.DropPending(channelID, pendingID))
}

func (w *Worker) Edit(ctx context.Context, postID, message string) error {
	if strings.TrimSpace(message) == "" {
		return ErrEmptyMessage
	}
	p, err := w.rc.PatchPost(ctx, postID, message)
	if err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.ApplyPostUpdate(p))
	return nil
}

func (w *Worker) Delete(ctx context.Context, postID string) error {
	if err := w.rc.DeletePost(ctx, postID); err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.RemovePost(postID))
	return nil
}

func (w *Worker) MarkUnread(ctx context.Context, postID string) error {
	u, err := w.rc.SetUnread(ctx, postID)
	if err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.SetUnread(u))
	return nil
}

func (w *Worker) SaveDraft(channelID, text string) { w.st.SetDraft(channelID, text) }

func (w *Worker) actionErr(err error) error {
	if sessionExpired(err) {
		w.signalAuth()
	}
	return err
}
