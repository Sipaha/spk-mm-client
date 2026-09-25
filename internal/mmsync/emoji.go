package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

const (
	emojiPageSize = 200 // the server's per_page cap
	maxEmojiPages = 20
	emojiMissTTL  = 10 * time.Minute
	maxEmojiMiss  = 1000
)

// REST is the server's REST client — its token and its rate limiter, shared
// with sync. Media streams go through it.
func (w *Worker) REST() *rest.Client { return w.rc }

// startEmojiLoad reads the server's custom emoji once per worker, in the
// background, after a bootstrap; a failed read is retried after the next.
func (w *Worker) startEmojiLoad() {
	if !w.st.CustomEmojiEnabled() || !w.emojiLoad.CompareAndSwap(false, true) {
		return
	}
	started := w.goBG(func(ctx context.Context) {
		if err := w.loadCustomEmoji(ctx); err != nil {
			w.emojiLoad.Store(false)
			if sessionExpired(err) {
				w.signalAuth()
			}
			slog.Warn("custom emoji unavailable", "srv", w.srv.ID, "err", err)
		}
	})
	if !started {
		w.emojiLoad.Store(false)
	}
}

func (w *Worker) loadCustomEmoji(ctx context.Context) error {
	var all []model.Emoji
	for page := 0; page < maxEmojiPages; page++ {
		list, err := w.rc.CustomEmojiPage(ctx, page, emojiPageSize)
		if err != nil {
			return err
		}
		all = append(all, list...)
		if len(list) < emojiPageSize {
			break
		}
	}
	w.st.SetCustomEmoji(all)
	return nil
}

// EmojiID resolves a custom emoji name for media: from the list (kept
// current by emoji_added), else one GET /emoji/name/{name}. A name the
// server does not know is remembered for emojiMissTTL, so a message full of
// ":not_an_emoji:" costs one request.
func (w *Worker) EmojiID(ctx context.Context, name string) (string, error) {
	if id, ok := w.st.CustomEmojiID(name); ok {
		return id, nil
	}
	if !w.st.CustomEmojiEnabled() {
		return "", nil
	}
	now := w.cfg.Now()
	w.missMu.Lock()
	at, missed := w.emojiMiss[name]
	w.missMu.Unlock()
	if missed && now.Sub(at) < emojiMissTTL {
		return "", nil
	}
	e, err := w.rc.EmojiByName(ctx, name)
	var re *rest.Error
	if errors.As(err, &re) && (re.Status == http.StatusNotFound || re.Status == http.StatusNotImplemented) {
		w.missMu.Lock()
		if len(w.emojiMiss) >= maxEmojiMiss {
			w.emojiMiss = map[string]time.Time{}
		}
		w.emojiMiss[name] = now
		w.missMu.Unlock()
		return "", nil
	}
	if err != nil {
		return "", err
	}
	w.st.AddCustomEmoji(e)
	return e.ID, nil
}
