package mmsync

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/state"
)

// Thread focus (spec «Поиск», Секция 1б; state/threadfocus.go): the open
// thread shown around a reply beyond those it holds. Its operations — a
// focus opened (OpenThreadAt), a page into it (LoadThreadFocus) and the
// reread of a stale focus — run one at a time in the thread lane, like a
// channel's history operations (jump.go): opening a focus cancels every one
// begun before, a user's page pre-empts a reread. Only the open thread has
// a focus, so one lane serves them all; the state's focus generation drops
// a page that lands anyway.

// ErrWrongThread is a reply focused that is not of that thread.
var ErrWrongThread = errors.New("mmsync: reply of another thread")

// threadLane is the lane of the thread focus (not a channel id).
const threadLane = "\x00thread"

// focusPage: replies loaded on each side of a reply focused.
const focusPage = 30

// OpenThreadAt opens rootID's thread in the panel showing replyID: a reply
// the thread holds already (or the root) is shown as it is, with no
// request; otherwise the segment around it (GET /posts/{id} — its channel
// and root checked — then direction=up/down pages of focusPage) becomes the
// thread's focus, the latest replies its tail. Either way every focus
// operation begun before is cancelled.
func (w *Worker) OpenThreadAt(ctx context.Context, channelID, rootID, replyID string) (state.ThreadView, error) {
	// Last (after the lane is let go of): a stale focus — whose reread this
	// cancelled, if one ran — is reread again.
	defer w.scheduleFocusRevalidation(rootID)
	l, ctx, end := w.histBegin(ctx, threadLane, opJump)
	defer end()
	op, held, ok := w.st.FocusThread(channelID, rootID, replyID)
	if !ok {
		return state.ThreadView{}, ErrNoChannel
	}
	w.threadOpened(rootID)
	if held {
		v, _ := w.st.ThreadView(rootID)
		return v, nil
	}
	w.changed(state.Change{Threads: []string{rootID}}) // a focus it had is gone
	if err := l.lock(ctx); err != nil {
		return state.ThreadView{}, w.histErr(ctx, err)
	}
	defer l.unlock()
	p, err := w.rc.Post(ctx, replyID)
	if err != nil {
		return state.ThreadView{}, w.jumpErr(ctx, err)
	}
	switch {
	case p.DeleteAt != 0:
		return state.ThreadView{}, ErrPostGone
	case p.ChannelID != channelID:
		return state.ThreadView{}, ErrWrongChannel
	case p.RootID != rootID:
		return state.ThreadView{}, ErrWrongThread
	}
	upQ := rest.ThreadQuery{PerPage: focusPage, FromCreateAt: p.CreateAt, FromPost: p.ID, CollapsedThreads: op.CRT}
	downQ := upQ
	downQ.Down = true
	var up, down model.PostList
	var uerr, derr error
	var wg sync.WaitGroup
	wg.Go(func() { up, uerr = w.rc.PostThread(ctx, rootID, upQ) })
	down, derr = w.rc.PostThread(ctx, rootID, downQ)
	wg.Wait()
	if err := errors.Join(uerr, derr); err != nil {
		return state.ThreadView{}, w.histErr(ctx, err)
	}
	if ctx.Err() != nil { // a newer navigation (the generation guards too)
		return state.ThreadView{}, w.histErr(ctx, ctx.Err())
	}
	if !w.st.SetThreadFocus(op, p, up, down) {
		return state.ThreadView{}, ErrSuperseded
	}
	w.focusLoaded(ctx, rootID)
	v, _ := w.st.ThreadView(rootID)
	return v, nil
}

// threadOpened: rootID was just opened in the panel — its latest page read
// if needed, its read state sent, its authors' statuses asked for.
func (w *Worker) threadOpened(rootID string) {
	if _, _, need := w.st.ThreadFetch(rootID); need {
		w.loadThread(rootID)
	}
	w.readThread(rootID)
	w.requestStatuses()
}

// focusLoaded: a focus page was applied — its authors' profiles and
// statuses, and the UI told.
func (w *Worker) focusLoaded(ctx context.Context, rootID string) {
	w.loadUsers(ctx)
	if w.st.OpenThreadID() == rootID {
		w.requestStatuses()
	}
	w.changed(state.Change{Threads: []string{rootID}})
}

// LoadThreadFocus loads a page (ThreadPage) into rootID's focus: older
// replies from its up edge, or newer ones from its down edge into the gap
// to the tail — the window slides past ThreadMaxReplies. A page that moves
// nothing is asked once more — a second in a row is ErrNoProgress. A page a
// newer focus (or its end) superseded is not an error. A stale focus is
// reread in the background once it is done.
func (w *Worker) LoadThreadFocus(ctx context.Context, rootID string, newer bool) error {
	defer w.scheduleFocusRevalidation(rootID)
	l, ctx, end := w.histBegin(ctx, threadLane, opUser)
	defer end()
	if err := l.lock(ctx); err != nil {
		return quietSuperseded(w.histErr(ctx, err))
	}
	defer l.unlock()
	for range 2 {
		op, ok := w.st.BeginFocusLoad(rootID, newer)
		if !ok {
			return nil
		}
		page, err := w.rc.PostThread(ctx, rootID, rest.ThreadQuery{PerPage: state.ThreadPage, FromCreateAt: op.Cursor.CreateAt,
			FromPost: op.Cursor.ID, CollapsedThreads: op.CRT, Down: newer})
		if err != nil {
			return quietSuperseded(w.histErr(ctx, err))
		}
		if _, progressed := w.st.AppendFocus(op, newer, page); progressed {
			w.focusLoaded(ctx, rootID)
			return nil
		}
	}
	return ErrNoProgress
}

// scheduleFocusRevalidation rereads rootID's stale focus in the background
// (after a reconnect, once the tail was read again; after LoadThreadFocus,
// OpenThreadAt and OpenThread), a failed reread retried revalRetries times.
func (w *Worker) scheduleFocusRevalidation(rootID string) {
	w.scheduleReread(threadLane, revalRetries, func() bool { return w.st.FocusStale(rootID) },
		func(ctx context.Context) error { return w.revalidateFocus(ctx, rootID) })
}

// revalidateFocus rereads the stale focus of rootID under the thread lane's
// lock (the channel segment's algorithm, revalidate). An error or a cancel
// leaves it stale with nothing removed; a reread the state dropped (a
// reconnect during it) is begun again.
func (w *Worker) revalidateFocus(ctx context.Context, rootID string) error {
	defer w.changed(state.Change{Threads: []string{rootID}})
	for range revalRetries {
		op, ok := w.st.BeginFocusRevalidate(rootID)
		if !ok {
			return nil
		}
		r, err := w.rereadFocus(ctx, op)
		if err != nil {
			return w.histErr(ctx, err)
		}
		applied := w.st.ApplyFocusRevalidation(op, r)
		w.loadUsers(ctx)
		if applied {
			return nil
		}
	}
	return nil
}

// rereadFocus reads op's held range as the server has it now: High (404:
// deleted), then direction=up pages from it down past Low (strictly older:
// replies sharing Low's create_at are all read) or to the thread's first
// reply — covered. Thread pages go by (create_at, id) from the cursor
// itself, not by a row the cursor names: a deleted cursor or ties at a
// page's bound hide nothing (unlike before= in a channel).
func (w *Worker) rereadFocus(ctx context.Context, op state.FocusRevalOp) (state.Reread, error) {
	var r state.Reread
	p, err := w.rc.Post(ctx, op.High.ID)
	var re *rest.Error
	switch {
	case errors.As(err, &re) && re.Status == http.StatusNotFound:
		r.HighGone = true
	case err != nil:
		return r, err
	default:
		r.High = &p
	}
	cur := op.High
	for {
		page, err := w.rc.PostThread(ctx, op.Root, rest.ThreadQuery{PerPage: state.ThreadMaxReplies, FromCreateAt: cur.CreateAt,
			FromPost: cur.ID, CollapsedThreads: op.CRT})
		if err != nil {
			return r, err
		}
		r.Pages = append(r.Pages, page)
		var oldest *model.Post
		for _, q := range page.Posts {
			if q.RootID == op.Root && (oldest == nil || q.CreateAt < oldest.CreateAt ||
				(q.CreateAt == oldest.CreateAt && q.ID < oldest.ID)) {
				oldest = &q
			}
		}
		more := page.HasNext != nil && *page.HasNext
		switch {
		case !more || (oldest != nil && oldest.CreateAt < op.Low.CreateAt):
			r.Covered = true
			return r, nil
		case oldest == nil:
			return r, ErrNoProgress // "more" and nothing: not proof
		}
		cur = state.Cursor{ID: oldest.ID, CreateAt: oldest.CreateAt}
	}
}

// RetryThreadRevalidation rereads rootID's stale focus now (the gap row's
// retry). A user's LoadThreadFocus pre-empts it like the background reread:
// it then returns nil, the focus still stale.
func (w *Worker) RetryThreadRevalidation(ctx context.Context, rootID string) error {
	return quietSuperseded(w.rereadOnce(ctx, threadLane, func(ctx context.Context) error { return w.revalidateFocus(ctx, rootID) }))
}
