package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

var (
	ErrNoPost   = errors.New("mmsync: post not loaded")
	ErrBadEmoji = errors.New("mmsync: invalid emoji name")
)

// The server's rule for reaction names (model.Reaction.IsValid).
var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)

const reactAttempts = 4 // tries of one reaction request before it is rolled back

// defaultReactBackoff: the waits between the tries of a reaction request
// whose outcome was unknown, while the worker stays live (going live again
// retries at once). Config.reactBackoff overrides it in tests.
var defaultReactBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}

// reactPair is our reaction on one post+emoji while it is being sent or
// waits for a retry. Guarded by Worker.reactMu.
type reactPair struct {
	postID, emoji string
	want          bool      // the user's last click
	server        bool      // the state the server last confirmed
	running       bool      // a React call or the retry loop is sending it
	unsure        bool      // the last request's outcome is unknown: server may be either state
	attempts      int       // tries that ended with an unknown outcome
	due           time.Time // waiting: the next try while live
}

// React adds or removes our reaction. The post changes at once; the WS
// echo is idempotent and a late echo of an earlier click is dropped (state
// intents). Clicks on a post+emoji whose request is in flight or waiting for
// a retry are not sent in parallel: the last one wins and is sent afterwards
// (add, remove, add while the first add runs → nothing more to send). After
// a request with an unknown outcome the server may hold either state, so
// even a click back to the confirmed state is sent — at once when the pair
// is waiting — and the pinned intent drops the late echo of the earlier one.
//
// The local change and the want it queues are made together under reactMu,
// so concurrent clicks cannot leave the post showing one click while another
// is sent. No lock is held across a request, and each request gets its own
// timeout, detached from the first caller: its page going away must not
// fail the clicks queued behind it.
//
// Failures: a refusal (an HTTP 4xx) sets the post back to the state the
// server last confirmed and is returned. An unknown outcome (network error,
// timeout, 5xx) keeps the click — shown, its intent pinned — for reactLoop
// to retry; only after reactAttempts tries is it rolled back.
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
	var mine *reactPair // set when this call sends
	switch p := w.reactPairs[key]; {
	case p != nil && p.running: // its sender picks the new want up
		p.want = add
	case p != nil: // waiting (unsure): the retry sends this click
		p.want = add
		w.st.PinReactIntent(postID, emoji, true)
		if add == p.server {
			// Back to the confirmed state, but the earlier request may have
			// landed: its opposite goes out now, not after the backoff.
			p.due = time.Now()
			w.pokeReactions()
		}
	case add == was: // nothing to send: no echo will end the intent
		w.st.ForgetReactIntent(postID, emoji)
	default:
		mine = &reactPair{postID: postID, emoji: emoji, want: add, server: was, running: true}
		w.reactPairs[key] = mine
	}
	w.reactMu.Unlock()
	w.changed(ch)
	if mine == nil {
		return nil
	}
	return w.sendPair(ctx, key, mine)
}

// sendPair sends the pair until the server has the user's last click, the
// server refuses it (rolled back, returned), or the outcome is unknown
// (left waiting for a retry, nil). The caller has set p.running.
func (w *Worker) sendPair(ctx context.Context, key string, p *reactPair) error {
	sentAny := false
	for {
		w.reactMu.Lock()
		want := p.want
		if want == p.server && !p.unsure {
			delete(w.reactPairs, key)
			if !sentAny { // no request of this run carries the last click's echo
				w.st.ForgetReactIntent(p.postID, p.emoji)
			}
			w.reactMu.Unlock()
			return nil
		}
		if w.life.Err() != nil { // stopping: nothing is sent any more
			p.running = false
			w.reactMu.Unlock()
			return nil
		}
		w.reactMu.Unlock()
		// A retried POST is the documented exception to "POST is not
		// retried on network errors": saving an existing reaction returns
		// 200 and deleting a missing one is harmless (api facts §7.4), so a
		// request that did land is not doubled.
		err := w.sendReaction(ctx, p.postID, p.emoji, want)
		if err == nil {
			if want {
				w.bumpRecent(p.emoji)
			}
			w.reactMu.Lock()
			p.server, p.attempts, p.unsure = want, 0, false
			w.st.PinReactIntent(p.postID, p.emoji, false) // its echo is due now
			w.reactMu.Unlock()
			sentAny = true
			continue
		}
		w.reactMu.Lock()
		if !refused(err) {
			p.attempts++
		}
		if refused(err) || p.attempts >= reactAttempts {
			delete(w.reactPairs, key)
			back := w.st.SetMyReaction(p.postID, p.emoji, p.server)
			w.reactMu.Unlock()
			w.changed(back)
			slog.Warn("reaction not sent", "srv", w.srv.ID, "post", p.postID, "emoji", p.emoji, "err", err)
			return w.actionErr(err)
		}
		backoff := w.cfg.reactBackoff[min(p.attempts, len(w.cfg.reactBackoff))-1]
		p.running, p.due, p.unsure = false, time.Now().Add(backoff), true
		w.st.PinReactIntent(p.postID, p.emoji, true)
		w.reactMu.Unlock()
		slog.Info("reaction outcome unknown, will retry", "srv", w.srv.ID, "post", p.postID, "in", backoff, "err", err)
		w.pokeReactions()
		return nil
	}
}

// refused: the server answered with a 4xx — it did not apply the request.
func refused(err error) bool {
	var re *rest.Error
	return errors.As(err, &re) && re.Status >= 400 && re.Status < 500
}

// pokeReactions makes reactLoop look at the waiting pairs again.
func (w *Worker) pokeReactions() {
	select {
	case w.reactPoke <- struct{}{}:
	default:
	}
}

// retryReactionsNow retries every waiting pair at once (the worker went
// live again).
func (w *Worker) retryReactionsNow() {
	select {
	case w.reactNow <- struct{}{}:
	default:
	}
}

// reactLoop retries the waiting reaction pairs, one at a time, while the
// worker is live: each when its backoff is over, all of them when the
// worker goes live again. It is one goroutine per worker and ends with Run.
// Leaving live needs no signal: a timer that fires then finds the worker
// not live, and the next turn arms none.
func (w *Worker) reactLoop(ctx context.Context) {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		t.Stop()
		// Offline, waiting pairs wait for the worker to go live again
		// (reactNow); a timer armed for them would spin.
		if next, ok := w.nextReactDue(); ok && w.Status() == StatusLive {
			t.Reset(time.Until(next))
		}
		all := false
		select {
		case <-ctx.Done():
			return
		case <-w.reactPoke:
			continue
		case <-w.reactNow:
			all = true
		case <-t.C:
		}
		if w.Status() == StatusLive {
			w.retryReactions(ctx, all)
		}
	}
}

func (w *Worker) nextReactDue() (time.Time, bool) {
	w.reactMu.Lock()
	defer w.reactMu.Unlock()
	var next time.Time
	for _, p := range w.reactPairs {
		if !p.running && (next.IsZero() || p.due.Before(next)) {
			next = p.due
		}
	}
	return next, !next.IsZero()
}

func (w *Worker) retryReactions(ctx context.Context, all bool) {
	now := time.Now()
	type job struct {
		key string
		p   *reactPair
	}
	var jobs []job
	w.reactMu.Lock()
	for k, p := range w.reactPairs {
		if !p.running && (all || !p.due.After(now)) {
			p.running = true
			jobs = append(jobs, job{k, p})
		}
	}
	w.reactMu.Unlock()
	for _, j := range jobs {
		if ctx.Err() != nil {
			w.reactMu.Lock()
			j.p.running = false
			w.reactMu.Unlock()
			continue
		}
		_ = w.sendPair(ctx, j.key, j.p) // failures are logged and rolled back there
	}
}

// callCtx bounds one reaction request: its own timeout, not cancelled with
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

func (w *Worker) bumpRecent(emoji string) {
	pref := w.st.BumpRecentEmoji(emoji)
	w.goBG(func(ctx context.Context) {
		if err := w.rc.SavePreferences(ctx, []model.Preference{pref}); err != nil {
			slog.Warn("could not save recent emoji", "srv", w.srv.ID, "err", err)
		}
	})
}
