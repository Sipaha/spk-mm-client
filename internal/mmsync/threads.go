package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/state"
)

// threadLoadTries bounds the reads of one thread in a row: a page dropped
// because the epoch moved meanwhile (ResetThreads, MarkStale) is read again.
const threadLoadTries = 3

// OpenThread opens rootID's thread in the panel: the cached view at once
// (Loaded=false while the first page is read), the page in the background —
// thread_changed follows. ok=false: channelID is not known.
func (w *Worker) OpenThread(channelID, rootID string) (state.ThreadView, bool) {
	_, need, ok := w.st.OpenThread(channelID, rootID)
	if !ok {
		return state.ThreadView{}, false
	}
	if need {
		w.loadThread(rootID)
	}
	w.requestStatuses() // the thread's authors
	return w.st.ThreadView(rootID)
}

// CloseThread closes the panel; the thread stays cached, trimmed.
func (w *Worker) CloseThread() { w.st.CloseThread() }

// openThreads is the open thread as a Change.Threads list (none: nil).
func (w *Worker) openThreads() []string {
	if root := w.st.OpenThreadID(); root != "" {
		return []string{root}
	}
	return nil
}

// reloadOpenThread reads the open thread again if it needs it (after a
// bootstrap: stale since the gap, or reset by a CRT switch).
func (w *Worker) reloadOpenThread() {
	if root := w.st.OpenThreadID(); root != "" {
		w.loadThread(root)
	}
}

// loadThread reads the latest page of a thread in the background, one
// request per thread at a time (like view); a need that arises while one
// runs is served by it afterwards.
func (w *Worker) loadThread(rootID string) {
	if _, busy := w.threadLoads.LoadOrStore(rootID, true); busy {
		return
	}
	if !w.goBG(func(ctx context.Context) { w.loadThreadLoop(ctx, rootID) }) {
		w.threadLoads.Delete(rootID)
	}
}

func (w *Worker) loadThreadLoop(ctx context.Context, rootID string) {
	tries := 0
	for {
		for ; tries < threadLoadTries && ctx.Err() == nil; tries++ {
			crt, epoch, need := w.st.ThreadFetch(rootID)
			if !need {
				break
			}
			if !w.fetchThread(ctx, rootID, crt, epoch) {
				tries = threadLoadTries // failed: shown; the user retries
			}
		}
		w.threadLoads.Delete(rootID)
		// A caller that found us busy after our last check relies on this
		// one: its need is visible now.
		if tries >= threadLoadTries || ctx.Err() != nil {
			return
		}
		if _, _, need := w.st.ThreadFetch(rootID); !need {
			return
		}
		if _, busy := w.threadLoads.LoadOrStore(rootID, true); busy {
			return
		}
	}
}

// fetchThread reads the latest page: GET …/thread with perPage (never
// without: the server would send the whole thread), direction=up. false:
// it failed (recorded on the thread) or the worker is stopping.
func (w *Worker) fetchThread(ctx context.Context, rootID string, crt bool, epoch uint64) bool {
	l, err := w.rc.PostThread(ctx, rootID, rest.ThreadQuery{PerPage: state.ThreadPage, CollapsedThreads: crt})
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Debug("thread fetch failed", "srv", w.srv.ID, "root", rootID, "err", err)
		w.st.FailThread(rootID, epoch, threadErrCode(err))
		w.changed(state.Change{Threads: []string{rootID}})
		return false
	}
	w.st.SetThreadPage(rootID, epoch, l)
	w.loadUsers(ctx)
	if w.st.OpenThreadID() == rootID {
		w.requestStatuses()
	}
	w.changed(state.Change{Threads: []string{rootID}})
	return true
}

// threadErrCode maps a failed thread read to the code the panel shows
// (the api layer's codes; 404 means the root is gone).
func threadErrCode(err error) string {
	var re *rest.Error
	switch {
	case errors.As(err, &re) && re.Status == http.StatusNotFound:
		return state.ThreadNotFound
	case errors.As(err, &re) && re.Status == http.StatusUnauthorized:
		return "session_expired"
	case errors.As(err, &re) && re.Status == http.StatusForbidden:
		return "forbidden"
	case rest.IsNetwork(err):
		return "unreachable"
	}
	return "internal"
}

// LoadOlderReplies reads the page before the oldest reply held
// (fromCreateAt/fromPost), until the thread holds ThreadMaxReplies; past
// that (Capped) nothing is requested.
func (w *Worker) LoadOlderReplies(ctx context.Context, rootID string) error {
	// The epoch first: a reset after it drops the page.
	crt, epoch, _ := w.st.ThreadFetch(rootID)
	v, ok := w.st.ThreadView(rootID)
	if !ok {
		return ErrNoPost
	}
	if !v.HasMore {
		return nil
	}
	id, at := w.st.OldestReply(rootID)
	if id == "" {
		return nil
	}
	l, err := w.rc.PostThread(ctx, rootID, rest.ThreadQuery{PerPage: state.ThreadPage, FromCreateAt: at, FromPost: id, CollapsedThreads: crt})
	if err != nil {
		return w.actionErr(err)
	}
	w.st.AppendOlderReplies(rootID, epoch, l)
	w.loadUsers(ctx)
	if w.st.OpenThreadID() == rootID {
		w.requestStatuses()
	}
	w.changed(state.Change{Threads: []string{rootID}})
	return nil
}
