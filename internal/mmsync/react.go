package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"regexp"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

var (
	ErrNoPost   = errors.New("mmsync: post not loaded")
	ErrBadEmoji = errors.New("mmsync: invalid emoji name")
)

// The server's rule for reaction names (model.Reaction.IsValid).
var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)

// React adds or removes our reaction. The post changes at once and is
// rolled back to the server's state if the server refuses; the WS echo is
// idempotent and a late echo of an earlier click is dropped (state
// intents). Clicks on a post+emoji whose request is still in flight are not
// sent in parallel: the last one wins and is sent when the request returns
// (add, remove, add while the first add runs → nothing more to send).
//
// The local change and the want it queues are made together under reactMu
// (and the rollback too), so concurrent clicks cannot leave the post showing
// one click while another is sent. No lock is held across a request.
func (w *Worker) React(ctx context.Context, postID, emoji string, add bool) error {
	if !emojiNameRe.MatchString(emoji) {
		return ErrBadEmoji
	}
	key := postID + "/" + emoji
	w.reactMu.Lock()
	ch, ok := w.st.ReactLocal(postID, emoji, add)
	if !ok {
		w.reactMu.Unlock()
		return ErrNoPost
	}
	_, busy := w.reactWant[key]
	w.reactWant[key] = add // when busy, the request in flight sends it afterwards
	w.reactMu.Unlock()
	w.changed(ch)
	if busy {
		return nil
	}
	sent := !add // the server's state before this click
	for {
		w.reactMu.Lock()
		want := w.reactWant[key]
		if want == sent {
			delete(w.reactWant, key)
			w.reactMu.Unlock()
			return nil
		}
		w.reactMu.Unlock()
		if err := w.sendReaction(ctx, postID, emoji, want); err != nil {
			w.reactMu.Lock()
			delete(w.reactWant, key)
			undo := w.st.UndoReactLocal(postID, emoji, want) // back to what the server has
			w.reactMu.Unlock()
			w.changed(undo)
			slog.Warn("reaction refused", "srv", w.srv.ID, "post", postID, "emoji", emoji, "err", err)
			return w.actionErr(err)
		}
		sent = want
	}
}

func (w *Worker) sendReaction(ctx context.Context, postID, emoji string, add bool) error {
	me := w.st.Me().ID
	if !add {
		return w.rc.DeleteReaction(ctx, me, postID, emoji)
	}
	if _, err := w.rc.SaveReaction(ctx, model.Reaction{UserID: me, PostID: postID, EmojiName: emoji}); err != nil {
		return err
	}
	pref := w.st.BumpRecentEmoji(emoji)
	w.goBG(func(ctx context.Context) {
		if err := w.rc.SavePreferences(ctx, []model.Preference{pref}); err != nil {
			slog.Warn("could not save recent emoji", "srv", w.srv.ID, "err", err)
		}
	})
	return nil
}
