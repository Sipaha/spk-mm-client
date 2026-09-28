package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/state"
)

// threadLoadTries bounds the reads of one thread in a row: a page dropped
// because the epoch moved meanwhile (ResetThreads, MarkStale) is read again.
const threadLoadTries = 3

// OpenThread opens rootID's thread in the panel: the cached view at once
// (Loaded=false while the first page is read), the page in the background —
// thread_changed follows. ok=false: channelID is not known. A reply's id
// opens its root's thread: at once if the reply is held, else once its
// page shows its root_id (fetchThread).
func (w *Worker) OpenThread(channelID, rootID string) (state.ThreadView, bool) {
	rootID = w.st.ThreadRootOf(rootID)
	_, need, ok := w.st.OpenThread(channelID, rootID)
	if !ok {
		return state.ThreadView{}, false
	}
	if need {
		w.loadThread(rootID)
	}
	w.readThread(rootID) // CRT, focused: it is on screen
	w.requestStatuses()  // the thread's authors
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
// request per thread at a time (like view); a load asked for while one
// runs is served by it afterwards — even once it gave up (threadAgain: the
// user's retry after a failure).
func (w *Worker) loadThread(rootID string) {
	w.threadAgain.Store(rootID, true) // before the busy check: see loadThreadLoop
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
		w.threadAgain.Delete(rootID) // served by this round
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
		// A caller that found us busy set threadAgain before its busy
		// check, so after this Delete it is visible here: a fresh request
		// (tries start over). Otherwise a need that arose meanwhile (an
		// epoch bump) is served within the tries left.
		_, again := w.threadAgain.LoadAndDelete(rootID)
		if ctx.Err() != nil {
			return
		}
		if again {
			tries = 0
		}
		if _, _, need := w.st.ThreadFetch(rootID); !need || tries >= threadLoadTries {
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
	if p, ok := l.Posts[rootID]; ok && p.RootID != "" {
		// rootID is a reply's (opened by a reply's id the feed no longer
		// held): its root's thread takes over.
		if _, need, ok := w.st.RedirectThread(rootID, p.RootID); ok && need {
			w.loadThread(p.RootID)
		}
		w.changed(state.Change{Threads: []string{rootID, p.RootID}})
		return true
	}
	w.st.SetThreadPage(rootID, epoch, l)
	w.loadUsers(ctx)
	if w.st.OpenThreadID() == rootID {
		w.requestStatuses()
	}
	w.changed(state.Change{Threads: []string{rootID}})
	// A read sent at opening (ts = our clock) may not cover replies the
	// page brought if the server's clock is ahead: they are newer than it.
	w.readThread(rootID)
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
	more, ok := w.st.ThreadHasMore(rootID)
	if !ok {
		return ErrNoPost
	}
	if !more {
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
	w.st.AppendOlderReplies(rootID, epoch, id, l)
	w.loadUsers(ctx)
	if w.st.OpenThreadID() == rootID {
		w.requestStatuses()
	}
	w.changed(state.Change{Threads: []string{rootID}})
	return nil
}

// threadReadTries bounds the reads of one thread in a row (new replies
// keep coming while each is sent), like view.
const threadReadTries = 3

// readThread marks the open thread read on the server if it should be now
// (state.ThreadReadTarget: CRT, the panel open over its channel, focused,
// followed, something new) — in the background, one request per thread at
// a time, checking again after each. Never retried on its own: a 404 (we
// do not follow it) stops the reads of this opening, any other failure is
// logged and waits for the next occasion.
func (w *Worker) readThread(rootID string) {
	if _, _, ok := w.st.ThreadReadTarget(rootID); !ok {
		return
	}
	if _, busy := w.threadReads.LoadOrStore(rootID, true); busy {
		return // the running loop checks again after its request
	}
	if !w.goBG(func(ctx context.Context) { w.readThreadLoop(ctx, rootID) }) {
		w.threadReads.Delete(rootID) // stopping: the read state is not sent
	}
}

func (w *Worker) readThreadLoop(ctx context.Context, rootID string) {
	for tries := 0; ; {
		for ; tries < threadReadTries && ctx.Err() == nil; tries++ {
			team, ts, ok := w.st.ThreadReadTarget(rootID)
			if !ok {
				break
			}
			if !w.markThreadRead(ctx, team, rootID, ts) {
				tries = threadReadTries
			}
		}
		w.threadReads.Delete(rootID)
		// An occasion that found us busy after our last check is served
		// here (within the tries left).
		if ctx.Err() != nil || tries >= threadReadTries {
			return
		}
		if _, _, ok := w.st.ThreadReadTarget(rootID); !ok {
			return
		}
		if _, busy := w.threadReads.LoadOrStore(rootID, true); busy {
			return
		}
	}
}

// markThreadRead sends one read; false: it failed (not retried). Its
// answer does not touch the mention counts: the thread_read_changed it
// causes does (counting both would count twice).
func (w *Worker) markThreadRead(ctx context.Context, team, rootID string, ts int64) bool {
	err := w.rc.MarkThreadRead(ctx, team, rootID, ts)
	var re *rest.Error
	switch {
	case err == nil:
		w.st.ThreadReadDone(rootID, ts)
		return true
	case ctx.Err() != nil:
	case errors.As(err, &re) && re.Status == http.StatusNotFound:
		slog.Debug("thread not followed, not marking it read", "srv", w.srv.ID, "root", rootID)
		w.st.ThreadNotFollowing(rootID)
	default:
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("thread mark read failed", "srv", w.srv.ID, "root", rootID, "err", err)
	}
	return false
}

// threadTotalsTeam is the team the DM/GM part of the thread totals is
// read through: the current one if still ours, else the first (DM/GM
// threads are in every team's totals); "" without teams.
func threadTotalsTeam(teams []model.Team, nav string) string {
	first := ""
	for _, t := range teams {
		if t.DeleteAt != 0 {
			continue
		}
		if t.ID == nav {
			return nav
		}
		if first == "" {
			first = t.ID
		}
	}
	return first
}

// fetchThreadCounts reads the mentions in followed threads: per team from
// teams/unread (which leaves DM/GM threads out), the DM/GM part ("") as
// team's totals with them minus without them, never below 0.
func (w *Worker) fetchThreadCounts(ctx context.Context, team string) (map[string]int64, error) {
	unread, err := w.rc.TeamsUnread(ctx, true)
	if err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(unread)+1)
	for _, u := range unread {
		m[u.TeamID] = u.ThreadMentionCount
	}
	if team == "" {
		return m, nil
	}
	all, err := w.rc.ThreadTotals(ctx, team, false)
	if err != nil {
		return nil, err
	}
	teamOnly, err := w.rc.ThreadTotals(ctx, team, true)
	if err != nil {
		return nil, err
	}
	m[""] = max(0, all.TotalUnreadMentions-teamOnly.TotalUnreadMentions)
	return m, nil
}

// rereadThreadCounts reads the thread mention totals again in the
// background (not under the refresh guard) and installs them whole — one
// reread at a time; one asked for meanwhile runs after it.
func (w *Worker) rereadThreadCounts() {
	w.countsAgain.Store(true)
	if !w.countsBusy.CompareAndSwap(false, true) {
		return
	}
	if !w.goBG(w.rereadThreadCountsLoop) {
		w.countsBusy.Store(false)
	}
}

func (w *Worker) rereadThreadCountsLoop(ctx context.Context) {
	for {
		w.countsAgain.Store(false)
		w.rereadThreadCountsOnce(ctx)
		w.countsBusy.Store(false)
		if ctx.Err() != nil || !w.countsAgain.Load() || !w.countsBusy.CompareAndSwap(false, true) {
			return
		}
	}
}

// threadCountsTries bounds the reads of the thread totals in a row while
// thread events keep overlapping them.
const threadCountsTries = 3

// rereadThreadCountsOnce: a read that a thread event overlapped may or may
// not include it — read again, up to threadCountsTries; the last one is
// installed anyway (never over a fresher metadata read).
func (w *Worker) rereadThreadCountsOnce(ctx context.Context) {
	for try := 1; try <= threadCountsTries && ctx.Err() == nil; try++ {
		crt, team, tok := w.st.ThreadCountsFetch()
		if !crt {
			return
		}
		m, err := w.fetchThreadCounts(ctx, team)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if sessionExpired(err) {
				w.signalAuth()
			}
			slog.Warn("thread mentions not reread", "srv", w.srv.ID, "err", err)
			return
		}
		installed, retry := w.st.SetThreadMentions(m, tok, try == threadCountsTries)
		if installed {
			w.changed(state.Change{Sidebar: true, Badge: true})
		}
		if !retry {
			return
		}
	}
}
