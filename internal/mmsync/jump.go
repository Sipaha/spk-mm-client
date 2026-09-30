package mmsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/state"
)

// History operations of the open channel (spec «Поиск», Секция 1): a jump
// to a post (JumpTo), a page above (LoadOlder), a page into the gap to the
// window (LoadNewer) and the reread of a stale segment run one at a time
// per channel — the channel's history lock (histLane.histMu) — and a jump
// cancels every one begun before it: it is a new navigation, whatever they
// would have loaded is not wanted. The state's generations (HistOp) drop a
// page that lands anyway.

var (
	// ErrPostGone means the post jumped to does not exist or was deleted (404).
	ErrPostGone = errors.New("mmsync: post deleted or not found")
	// ErrForbidden is a post jumped to that cannot be read (403).
	ErrForbidden = errors.New("mmsync: no access to the post")
	// ErrWrongChannel is a post jumped to that is not of that channel.
	ErrWrongChannel = errors.New("mmsync: post of another channel")
	// ErrNoProgress means two pages into the gap in a row moved nothing — the
	// user retries (no automatic loop).
	ErrNoProgress = errors.New("mmsync: loading newer posts makes no progress")
	// ErrSuperseded means a newer navigation (a jump, another channel opened)
	// replaced this one; nothing was applied.
	ErrSuperseded = errors.New("mmsync: superseded by a newer navigation")
)

// jumpPage: posts loaded on each side of a post jumped to.
const jumpPage = 30

// revalRetries bounds the rereads of a stale segment begun one after the
// other when each is dropped (a reconnect during it moves the gap
// generation); a failed one is not retried — the user or LoadNewer does.
const revalRetries = 3

// JumpResult is where a jump landed. InFeed=false: a reply of a collapsed
// thread (CRT) — the feed does not show it, the UI opens its thread.
type JumpResult struct {
	PostID string `json:"post_id"`
	RootID string `json:"root_id"`
	InFeed bool   `json:"in_feed"`
}

// histLane is one channel's history operations.
type histLane struct {
	histMu chan struct{} // held by the running operation (a lock a waiter can give up on)
	mu     sync.Mutex
	ops    map[*histTicket]struct{} // begun and not ended: a jump cancels them
	reval  bool                     // a background reread is queued or running
}

type histTicket struct{ cancel context.CancelCauseFunc }

func (w *Worker) lane(channelID string) *histLane {
	w.lanesMu.Lock()
	defer w.lanesMu.Unlock()
	l := w.lanes[channelID]
	if l == nil {
		l = &histLane{histMu: make(chan struct{}, 1), ops: map[*histTicket]struct{}{}}
		w.lanes[channelID] = l
	}
	return l
}

// histBegin registers a history operation of the channel: its ctx ends
// with the caller's, the worker's life, or a later jump (cause
// ErrSuperseded). A jump first cancels every operation begun before it.
// end must be called when the operation is over.
func (w *Worker) histBegin(ctx context.Context, channelID string, jump bool) (l *histLane, octx context.Context, end func()) {
	l = w.lane(channelID)
	octx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(w.life, func() { cancel(context.Canceled) })
	t := &histTicket{cancel: cancel}
	l.mu.Lock()
	if jump {
		for o := range l.ops {
			o.cancel(ErrSuperseded)
			delete(l.ops, o)
		}
	}
	l.ops[t] = struct{}{}
	l.mu.Unlock()
	return l, octx, func() {
		l.mu.Lock()
		delete(l.ops, t)
		l.mu.Unlock()
		stop()
		cancel(context.Canceled)
	}
}

// lock takes the channel's history lock, or gives up with ctx.
func (l *histLane) lock(ctx context.Context) error {
	select {
	case l.histMu <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if ctx.Err() != nil {
		<-l.histMu
		return context.Cause(ctx)
	}
	return nil
}

func (l *histLane) unlock() { <-l.histMu }

// histErr: an operation's error as its caller sees it — ErrSuperseded when
// a jump cancelled it, 401 sent down the re-auth path.
func (w *Worker) histErr(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), ErrSuperseded) {
		return ErrSuperseded
	}
	return w.actionErr(err)
}

// JumpTo shows postID of the open channel: a post held already is shown as
// it is (no request), else the segment around it replaces the held history
// (Post first — its channel and thread; then before=/after= pages of
// jumpPage). Either way it is a new navigation: every history operation
// begun before is cancelled and its page dropped.
func (w *Worker) JumpTo(ctx context.Context, channelID, postID string) (JumpResult, error) {
	l, ctx, end := w.histBegin(ctx, channelID, true)
	defer end()
	op, ok := w.st.BeginJump(channelID)
	if !ok {
		return JumpResult{}, ErrNoChannel
	}
	if p, ok := w.st.HeldPost(channelID, postID); ok {
		return JumpResult{PostID: p.ID, RootID: p.RootID, InFeed: true}, nil
	}
	if err := l.lock(ctx); err != nil {
		return JumpResult{}, w.histErr(ctx, err)
	}
	defer l.unlock()
	p, err := w.rc.Post(ctx, postID)
	if err != nil {
		return JumpResult{}, w.jumpErr(ctx, err)
	}
	switch {
	case p.DeleteAt != 0:
		return JumpResult{}, ErrPostGone
	case p.ChannelID != channelID:
		return JumpResult{}, ErrWrongChannel
	case op.CRT && p.RootID != "":
		// A collapsed reply: its thread shows it; the feed stays.
		return JumpResult{PostID: p.ID, RootID: p.RootID}, nil
	}
	var before, after model.PostList
	var berr, aerr error
	var wg sync.WaitGroup
	wg.Go(func() {
		before, berr = w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: jumpPage, Before: postID, CollapsedThreads: op.CRT})
	})
	after, aerr = w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: jumpPage, After: postID, CollapsedThreads: op.CRT})
	wg.Wait()
	if err := errors.Join(berr, aerr); err != nil {
		return JumpResult{}, w.histErr(ctx, err)
	}
	if !w.st.SetSegment(op, p, before, after) {
		return JumpResult{}, ErrSuperseded
	}
	w.historyLoaded(ctx, channelID)
	return JumpResult{PostID: p.ID, RootID: p.RootID, InFeed: true}, nil
}

// jumpErr: GET /posts/{id}'s 404 and 403 as the jump's own errors.
func (w *Worker) jumpErr(ctx context.Context, err error) error {
	var re *rest.Error
	switch {
	case errors.As(err, &re) && re.Status == http.StatusNotFound:
		return fmt.Errorf("%w: %w", ErrPostGone, err)
	case errors.As(err, &re) && re.Status == http.StatusForbidden:
		return fmt.Errorf("%w: %w", ErrForbidden, err)
	}
	return w.histErr(ctx, err)
}

// historyLoaded: a history page was applied — its authors' profiles and
// statuses, and the UI told.
func (w *Worker) historyLoaded(ctx context.Context, channelID string) {
	w.loadUsers(ctx)
	if channelID == w.st.Active() {
		w.requestStatuses()
	}
	w.changed(state.Change{Channels: []string{channelID}})
}

// LoadOlder loads a page of history above the open channel from the raw
// cursor BeginLoadOlder captures — under the history lock, so after the
// operation before it applied its page. A page that no longer fits (a
// jump, a reset) is dropped; a jump cancelling it is not an error either.
func (w *Worker) LoadOlder(ctx context.Context, channelID string) error {
	l, ctx, end := w.histBegin(ctx, channelID, false)
	defer end()
	if err := l.lock(ctx); err != nil {
		return quietSuperseded(w.histErr(ctx, err))
	}
	defer l.unlock()
	op, ok := w.st.BeginLoadOlder(channelID)
	if !ok {
		return nil
	}
	page, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, Before: op.Cursor.ID, CollapsedThreads: op.CRT})
	if err != nil {
		return quietSuperseded(w.histErr(ctx, err))
	}
	w.st.AppendOlder(op, page)
	w.historyLoaded(ctx, channelID)
	return nil
}

func quietSuperseded(err error) error {
	if errors.Is(err, ErrSuperseded) {
		return nil
	}
	return err
}

// LoadNewer loads a page (WindowSize) into the gap between the held history
// and the window, from the history's raw after-cursor; closed: no gap is
// left (proved: AppendNewer). A stale segment is reread first. A page that
// moves nothing is asked once more — a second in a row is ErrNoProgress.
func (w *Worker) LoadNewer(ctx context.Context, channelID string) (closed bool, err error) {
	l, ctx, end := w.histBegin(ctx, channelID, false)
	defer end()
	if err := l.lock(ctx); err != nil {
		return false, quietSuperseded(w.histErr(ctx, err))
	}
	defer l.unlock()
	if w.st.SegmentStale(channelID) {
		// A failed reread leaves the segment stale (its row offers a
		// retry); the gap is loaded all the same.
		if err := w.revalidate(ctx, channelID); errors.Is(err, ErrSuperseded) || sessionExpired(err) {
			return false, quietSuperseded(err)
		}
	}
	for range 2 {
		op, ok := w.st.BeginLoadNewer(channelID)
		if !ok {
			return true, nil
		}
		page, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, After: op.Cursor.ID, CollapsedThreads: op.CRT})
		if err != nil {
			return false, quietSuperseded(w.histErr(ctx, err))
		}
		closed, progressed := w.st.AppendNewer(op, page)
		if closed || progressed {
			w.historyLoaded(ctx, channelID)
			return closed, nil
		}
	}
	return false, ErrNoProgress
}

// RetryRevalidation rereads the open channel's stale segment now (the gap
// row's retry).
func (w *Worker) RetryRevalidation(ctx context.Context, channelID string) error {
	l, ctx, end := w.histBegin(ctx, channelID, false)
	defer end()
	if err := l.lock(ctx); err != nil {
		return quietSuperseded(w.histErr(ctx, err))
	}
	defer l.unlock()
	return quietSuperseded(w.revalidate(ctx, channelID))
}

// scheduleRevalidation rereads the open channel's stale segment in the
// background (after a catch-up overflow), once at a time.
func (w *Worker) scheduleRevalidation(channelID string) {
	if !w.st.SegmentStale(channelID) {
		return
	}
	l := w.lane(channelID)
	l.mu.Lock()
	busy := l.reval
	l.reval = true
	l.mu.Unlock()
	if busy {
		return
	}
	done := func() {
		l.mu.Lock()
		l.reval = false
		l.mu.Unlock()
	}
	if !w.goBG(func(ctx context.Context) {
		defer done()
		_ = w.RetryRevalidation(ctx, channelID)
	}) {
		done()
	}
}

// revalidate rereads the stale segment of the open channel under its
// history lock (spec «Уточнения» п.2): the newest held post (404: deleted),
// then before= pages from it down past the oldest held one or to the
// channel's first post — only then is a held post missing from them gone.
// An error or a cancel leaves the segment stale with nothing removed. A
// reread dropped by the state (a reconnect during it) is begun again.
func (w *Worker) revalidate(ctx context.Context, channelID string) error {
	defer w.changed(state.Change{Channels: []string{channelID}})
	for range revalRetries {
		op, ok := w.st.BeginRevalidate(channelID)
		if !ok {
			return nil
		}
		high, highGone, pages, err := w.reread(ctx, channelID, op)
		if err != nil {
			return w.histErr(ctx, err)
		}
		applied := w.st.ApplyRevalidation(op, high, highGone, pages, true)
		w.loadUsers(ctx)
		if applied {
			return nil
		}
	}
	return nil
}

// reread: the held range of op as the server has it now, covered (see
// revalidate) — or an error.
func (w *Worker) reread(ctx context.Context, channelID string, op state.RevalOp) (high *model.Post, highGone bool, pages []model.PostList, err error) {
	p, err := w.rc.Post(ctx, op.High.ID)
	var re *rest.Error
	switch {
	case errors.As(err, &re) && re.Status == http.StatusNotFound:
		highGone = true
	case err != nil:
		return nil, false, nil, err
	default:
		high = &p
	}
	cur := op.High.ID
	for {
		page, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, Before: cur, CollapsedThreads: op.CRT})
		if err != nil {
			return nil, false, nil, err
		}
		pages = append(pages, page)
		asc := page.Ascending()
		switch {
		case page.PrevPostID == "":
			return high, highGone, pages, nil
		case len(asc) == 0:
			return nil, false, nil, errors.New("mmsync: an empty page before the channel's first post")
		case asc[0].CreateAt < op.Low.CreateAt:
			return high, highGone, pages, nil
		}
		cur = asc[0].ID
	}
}
