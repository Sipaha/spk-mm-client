package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

var (
	ErrNoPost   = errors.New("mmsync: post not loaded")
	ErrBadEmoji = errors.New("mmsync: invalid emoji name")
)

// The server's rule for reaction names (model.Reaction.IsValid).
var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)

// React adds or removes our reaction. The post changes at once; the WS
// echo is idempotent and a late echo of an earlier click is dropped (state
// intents). Clicks on a post+emoji whose request is still in flight are not
// sent in parallel: the last one wins and is sent when the request returns
// (add, remove, add while the first add runs → nothing more to send).
//
// The local change and the want it queues are made together under reactMu
// (and the rollback too), so concurrent clicks cannot leave the post showing
// one click while another is sent. No lock is held across a request, and
// each request gets its own timeout, detached from the first caller: its
// page going away must not fail the clicks queued behind it.
//
// Failures: a refusal (an HTTP 4xx) sets the post back to the state the
// server last confirmed. When the outcome is unknown (network error,
// timeout, 5xx) the server may have applied it and its echo may already
// have been spent, so the post's reactions are read back: the request
// counts as done if the server has what was asked, else the post takes the
// server's state.
func (w *Worker) React(ctx context.Context, postID, emoji string, add bool) error {
	if !emojiNameRe.MatchString(emoji) {
		return ErrBadEmoji
	}
	key := postID + "/" + emoji
	w.reactMu.Lock()
	ch, was, ok := w.st.ReactLocalWas(postID, emoji, add)
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
	sent := was // the server's state before this click, as far as we know
	for first := true; ; first = false {
		w.reactMu.Lock()
		want := w.reactWant[key]
		if want == sent {
			delete(w.reactWant, key)
			if first { // nothing was sent: no echo will end the click's intent
				w.st.ForgetReactIntent(postID, emoji)
			}
			w.reactMu.Unlock()
			return nil
		}
		w.reactMu.Unlock()
		err := w.sendReaction(ctx, postID, emoji, want)
		server := sent
		if err != nil && !refused(err) {
			if has, rerr := w.myReaction(ctx, postID, emoji); rerr == nil {
				server = has
				if has == want {
					slog.Info("reaction reply lost, the server has it", "srv", w.srv.ID, "post", postID, "err", err)
					err = nil
				}
			} else {
				slog.Warn("could not read reactions back", "srv", w.srv.ID, "post", postID, "err", rerr)
			}
		}
		if err == nil {
			if want {
				w.bumpRecent(emoji)
			}
			sent = want
			continue
		}
		w.reactMu.Lock()
		delete(w.reactWant, key)
		undo := w.st.SetMyReaction(postID, emoji, server)
		w.reactMu.Unlock()
		w.changed(undo)
		slog.Warn("reaction refused", "srv", w.srv.ID, "post", postID, "emoji", emoji, "err", err)
		return w.actionErr(err)
	}
}

// refused: the server answered with a 4xx — it did not apply the request.
func refused(err error) bool {
	var re *rest.Error
	return errors.As(err, &re) && re.Status >= 400 && re.Status < 500
}

// callCtx bounds one request of React: its own timeout, not cancelled with
// the caller's ctx, but with the worker.
func (w *Worker) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.CallTimeout)
	stop := context.AfterFunc(w.life, cancel)
	return c, func() { stop(); cancel() }
}

func (w *Worker) sendReaction(ctx context.Context, postID, emoji string, add bool) error {
	ctx, cancel := w.callCtx(ctx)
	defer cancel()
	me := w.st.Me().ID
	if !add {
		return w.rc.DeleteReaction(ctx, me, postID, emoji)
	}
	_, err := w.rc.SaveReaction(ctx, model.Reaction{UserID: me, PostID: postID, EmojiName: emoji})
	return err
}

// myReaction reads from the server whether our reaction is on the post.
func (w *Worker) myReaction(ctx context.Context, postID, emoji string) (bool, error) {
	ctx, cancel := w.callCtx(ctx)
	defer cancel()
	list, err := w.rc.PostReactions(ctx, postID)
	if err != nil {
		return false, err
	}
	me := w.st.Me().ID
	return slices.ContainsFunc(list, func(r model.Reaction) bool { return r.UserID == me && r.EmojiName == emoji }), nil
}

func (w *Worker) bumpRecent(emoji string) {
	pref := w.st.BumpRecentEmoji(emoji)
	w.goBG(func(ctx context.Context) {
		if err := w.rc.SavePreferences(ctx, []model.Preference{pref}); err != nil {
			slog.Warn("could not save recent emoji", "srv", w.srv.ID, "err", err)
		}
	})
}
