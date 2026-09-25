package mmsync

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultStatusEvery = time.Minute
	statusDebounce     = 300 * time.Millisecond
	maxStatusTargets   = 400 // 4 requests of 100 ids at most
)

// statusLoop polls the presence of users on screen: on going live, shortly
// after a channel opens (a burst of switches costs one poll) and every
// StatusEvery. Only while live — offline there is nobody to ask.
func (w *Worker) statusLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.StatusEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.statusDue:
			select {
			case <-ctx.Done():
				return
			case <-time.After(statusDebounce):
			}
			select {
			case <-w.statusDue:
			default:
			}
		}
		if w.Status() == StatusLive {
			w.pollStatuses(ctx)
		}
	}
}

func (w *Worker) pollStatuses(ctx context.Context) {
	ids := w.st.StatusTargets(maxStatusTargets)
	if len(ids) == 0 {
		return
	}
	list, err := w.rc.StatusesByIDs(ctx, ids)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Debug("statuses unavailable", "srv", w.srv.ID, "err", err)
		return
	}
	w.changed(w.st.SetPresence(list))
}

func (w *Worker) requestStatuses() {
	select {
	case w.statusDue <- struct{}{}:
	default:
	}
}
