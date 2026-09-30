package mmsync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/state"
)

// fetchQueue holds channels whose window must be loaded or caught up,
// popped by priority (then FIFO); pushing a queued channel again can only
// raise its priority.
type fetchQueue struct {
	mu    sync.Mutex
	items map[string]state.SyncItem
	order map[string]int64
	n     int64
	wake  chan struct{}
}

func newFetchQueue() *fetchQueue {
	return &fetchQueue{items: map[string]state.SyncItem{}, order: map[string]int64{}, wake: make(chan struct{}, 1)}
}

func (q *fetchQueue) push(it state.SyncItem) {
	q.mu.Lock()
	if cur, ok := q.items[it.ChannelID]; !ok || it.Priority < cur.Priority {
		if !ok {
			q.n++
			q.order[it.ChannelID] = q.n
		}
		q.items[it.ChannelID] = it
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *fetchQueue) pop(ctx context.Context) (state.SyncItem, bool) {
	for {
		q.mu.Lock()
		var best state.SyncItem
		found := false
		for id, it := range q.items {
			if !found || it.Priority < best.Priority || (it.Priority == best.Priority && q.order[id] < q.order[best.ChannelID]) {
				best, found = it, true
			}
		}
		if found {
			delete(q.items, best.ChannelID)
			delete(q.order, best.ChannelID)
		}
		more := len(q.items) > 0
		q.mu.Unlock()
		if found {
			if more { // let the next fetcher go
				select {
				case q.wake <- struct{}{}:
				default:
				}
			}
			return best, true
		}
		select {
		case <-ctx.Done():
			return state.SyncItem{}, false
		case <-q.wake:
		}
	}
}

func (w *Worker) enqueueAll() {
	for _, it := range w.st.SyncItems() {
		w.queue.push(it)
	}
}

func (w *Worker) fetchLoop(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.cfg.Fetchers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				it, ok := w.queue.pop(ctx)
				if !ok {
					return
				}
				w.fetch(ctx, it)
			}
		}()
	}
	wg.Wait()
}

func (w *Worker) fetch(ctx context.Context, queued state.SyncItem) {
	// The generation first: a ResetWindows after it drops the page, even
	// if the item below was read before the reset.
	crt, gen := w.st.FetchMode()
	it, need := w.st.SyncItemFor(queued.ChannelID) // the queued copy may be outdated
	if !need {
		return
	}
	started := w.cfg.Now().UnixMilli()
	var err error
	if it.Loaded {
		var l model.PostList
		l, err = w.rc.ChannelPosts(ctx, it.ChannelID, rest.PostsQuery{Since: max(1, it.SyncedAt-sinceMargin.Milliseconds()), CollapsedThreads: crt})
		switch {
		case err != nil:
		case len(l.Order) >= w.cfg.sinceLimit:
			err = w.loadLatest(ctx, it.ChannelID, crt, gen, started)
		default:
			w.st.MergeSince(it.ChannelID, l.Ascending(), started, gen)
		}
	} else {
		err = w.loadLatest(ctx, it.ChannelID, crt, gen, started)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if sessionExpired(err) {
			w.signalAuth()
			return
		}
		if rest.IsNetwork(err) {
			time.AfterFunc(retryFetchIn, func() {
				if ctx.Err() == nil {
					w.queue.push(it)
				}
			})
		}
		slog.Debug("channel fetch failed", "srv", w.srv.ID, "channel", it.ChannelID, "err", err)
		return
	}
	w.loadUsers(ctx)
	if it.ChannelID == w.st.Active() {
		w.requestStatuses() // authors the open channel did not have yet
		// A catch-up overflow left the held history stale (only the latest
		// page was reloaded): reread it. Also retries one a reconnect
		// dropped.
		w.scheduleRevalidation(it.ChannelID)
	}
	w.changed(state.Change{Sidebar: true, Channels: []string{it.ChannelID}})
}

func (w *Worker) loadLatest(ctx context.Context, channelID string, crt bool, gen uint64, started int64) error {
	l, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, CollapsedThreads: crt})
	if err != nil {
		return err
	}
	w.st.SetWindow(channelID, l.Ascending(), l.PrevPostID == "", started, gen)
	return nil
}
