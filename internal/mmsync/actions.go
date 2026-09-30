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

var (
	ErrEmptyMessage  = errors.New("mmsync: empty message")
	ErrNoAttachments = errors.New("mmsync: attachments are not enabled")
)

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
	if f {
		w.readThread(w.st.OpenThreadID()) // the open thread (CRT) is on screen now
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

// LoadOlder loads a page of history above the open channel from the raw
// cursor BeginLoadOlder captures with the history generations; a page that
// no longer fits (a jump, a reset, another page applied) is dropped.
func (w *Worker) LoadOlder(ctx context.Context, channelID string) error {
	op, ok := w.st.BeginLoadOlder(channelID)
	if !ok {
		return nil
	}
	l, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, Before: op.Cursor.ID, CollapsedThreads: op.CRT})
	if err != nil {
		return w.actionErr(err)
	}
	w.st.AppendOlder(op, l)
	w.loadUsers(ctx)
	if channelID == w.st.Active() {
		w.requestStatuses() // authors of the older posts
	}
	w.changed(state.Change{Channels: []string{channelID}})
	return nil
}

// Send shows the post at once (with its attachments' local files) and
// sends it in the background: once every attachment is uploaded, the post
// is created with their file ids. A text is needed only without files.
func (w *Worker) Send(channelID, message string, files ...state.FileView) error {
	return w.send(channelID, "", message, files...)
}

// SendReply is Send for a reply: rootID is CreatePost's root_id. The
// caller (api.Service) checks that rootID is a thread this worker's
// thread cache holds for channelID (state.Server.ThreadHeld) before
// calling — SendReply itself does not, the same way Send does not check
// that channelID is a channel we are in. The pending reply carries
// RootID, so applyNewPostLocked (via AddPending/PostCreated) puts it in
// the channel window (kept out of the CRT feed — Task 2) and the thread
// cache in one place; nothing here is thread-specific beyond that.
func (w *Worker) SendReply(channelID, rootID, message string, files ...state.FileView) error {
	return w.send(channelID, rootID, message, files...)
}

func (w *Worker) send(channelID, rootID, message string, files ...state.FileView) error {
	if strings.TrimSpace(message) == "" && len(files) == 0 {
		return ErrEmptyMessage
	}
	if len(files) > 0 && w.cfg.Files == nil {
		return ErrNoAttachments
	}
	p := w.st.AddPending(channelID, rootID, message, files...)
	ch := state.Change{Channels: []string{channelID}}
	if rootID != "" {
		ch.Threads = []string{rootID}
	}
	w.changed(ch)
	if len(files) > 0 {
		// Sending is the user asking: an upload that failed in the
		// composer is tried again.
		w.cfg.Files.Retry(attachmentIDs(p))
	}
	w.startCreate(p)
	return nil
}

func attachmentIDs(p state.Pending) []string {
	ids := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		ids = append(ids, f.ID)
	}
	return ids
}

// create waits for the post's uploads — without a deadline: offline they
// wait for the server to be live, and each fails on its own (error, stall,
// removal); the worker stopping ends the wait — then creates the post
// under createTimeout.
func (w *Worker) create(ctx context.Context, p state.Pending) {
	var fileIDs []string
	if len(p.Files) > 0 {
		ids, err := w.cfg.Files.Wait(ctx, attachmentIDs(p))
		if err != nil {
			slog.Warn("send failed: attachments not uploaded", "srv", w.srv.ID, "channel", p.ChannelID, "err", err)
			w.changed(w.st.FailPending(p.ChannelID, p.ID))
			return
		}
		fileIDs = ids
	}
	cctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	post, err := w.rc.CreatePost(cctx, model.Post{ChannelID: p.ChannelID, RootID: p.RootID, Message: p.Message,
		PendingPostID: p.ID, UserID: w.st.Me().ID, FileIDs: fileIDs})
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("send failed", "srv", w.srv.ID, "channel", p.ChannelID, "err", err)
		w.changed(w.st.FailPending(p.ChannelID, p.ID))
		if p.RootID != "" && isRootDeletedErr(err) {
			// The same app_error covers the root being deleted, an
			// unrelated store hiccup fetching it, and (defensively) "root
			// is itself a reply" (mm-10.11 post.go:296,305) — this alone
			// never proves deletion. Mark the thread stale so the next
			// load re-reads it; that reread's own 404 (fetchThread →
			// FailThread(ThreadNotFound) → rootGoneLocked) or success is
			// the real verdict.
			if w.st.MarkThreadStale(p.RootID) {
				w.changed(state.Change{Threads: []string{p.RootID}})
				w.loadThread(p.RootID)
			}
		}
		return
	}
	w.changed(w.st.PostCreated(post))
}

// isRootDeletedErr reports CreatePost's 400 for a reply whose root no
// longer exists — checked against mm-10.11
// server/channels/app/post.go:296,305 (also raised when the "root" is
// itself a reply: the server refuses reply-to-reply the same way; and for
// an unrelated store error fetching the root — see MarkThreadStale's
// caller above, which treats this as a hint, not a verdict).
func isRootDeletedErr(err error) bool {
	var re *rest.Error
	return errors.As(err, &re) && re.ID == "api.post.create_post.root_id.app_error"
}

// Retry resends a failed post with the same pending id: if the first
// attempt did reach the server, it returns the existing post (no duplicate).
// Its failed uploads are sent again; uploaded files keep their ids.
func (w *Worker) Retry(channelID, pendingID string) {
	p, ch, ok := w.st.RetryPending(channelID, pendingID)
	if !ok {
		return
	}
	w.changed(ch)
	if len(p.Files) > 0 {
		w.cfg.Files.Retry(attachmentIDs(p))
	}
	w.startCreate(p)
}

// startCreate sends p in the background; if the worker is stopping, p is
// marked failed at once instead of hanging as pending, and its attachments
// are let go of (Run has released those of its pending posts already).
func (w *Worker) startCreate(p state.Pending) {
	if !w.goBG(func(ctx context.Context) { w.create(ctx, p) }) {
		w.changed(w.st.FailPending(p.ChannelID, p.ID))
		if len(p.Files) > 0 {
			w.cfg.Files.Release(attachmentIDs(p))
		}
	}
}

// Discard drops a pending post; its attachments are let go of (uploads
// cancelled, spools deleted) through changed.
func (w *Worker) Discard(channelID, pendingID string) {
	w.changed(w.st.DropPending(channelID, pendingID))
}

// releaseFiles lets go of the attachments of pending posts that are gone
// (confirmed by the server, discarded, their channel left).
func (w *Worker) releaseFiles() {
	if w.cfg.Files == nil {
		return
	}
	if ids := w.st.TakeReleased(); len(ids) > 0 {
		w.cfg.Files.Release(ids)
	}
	w.releaseForgottenComposers()
}

// releaseForgottenComposers lets go of attachments still staged in a
// composer whose channel (or one of its threads) we no longer track —
// state.Server.TakeForgottenComposers, channel-leave only (see its doc).
func (w *Worker) releaseForgottenComposers() {
	if w.cfg.Files == nil {
		return
	}
	for _, k := range w.st.TakeForgottenComposers() {
		w.cfg.Files.ReleaseComposer(w.srv.ID, k.Channel, k.Root)
	}
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

// SetSaved saves/unsaves a post for later (the webapp's flagged_post
// preference, named by the post's id, value "true"; unsaving deletes it).
// Not retried on transport errors like every other write here; on success
// the state updates at once instead of waiting for the preferences_changed/
// preferences_deleted WS echo (mirrors Edit/Delete/MarkUnread).
func (w *Worker) SetSaved(ctx context.Context, postID string, saved bool) error {
	pref := model.Preference{UserID: w.st.Me().ID, Category: "flagged_post", Name: postID, Value: "true"}
	var err error
	if saved {
		err = w.rc.SavePreferences(ctx, []model.Preference{pref})
	} else {
		err = w.rc.DeletePreferences(ctx, []model.Preference{pref})
	}
	if err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.SetPostSaved(postID, saved))
	return nil
}

func (w *Worker) actionErr(err error) error {
	if sessionExpired(err) {
		w.signalAuth()
	}
	return err
}
