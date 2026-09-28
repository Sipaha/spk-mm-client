// Package mmsync keeps one Mattermost server in sync: a worker per signed-in
// server validates the session, holds the WebSocket (with resume), resyncs
// metadata when the event stream is lost, catches post windows up in the
// background, writes the cache snapshot behind and performs user actions.
package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/spk/spk-mm-client/internal/state"
	"github.com/spk/spk-mm-client/internal/store"
)

// Status is the connection state of a server shown in the UI.
type Status string

// Connection states: off → connecting → live → reconnecting → needs_reauth.
const (
	StatusOff          Status = "off"
	StatusConnecting   Status = "connecting"
	StatusLive         Status = "live"
	StatusReconnecting Status = "reconnecting"
	StatusNeedsReauth  Status = "needs_reauth"
)

// Hooks report a worker's changes to its owner. They run synchronously on
// the worker's goroutines (the event loop, fetchers, background actions),
// so they must return quickly and must never call the Manager's Start,
// Stop or Close synchronously: Stop waits for the worker to finish while
// holding the manager's lock, so a hook doing that deadlocks — hand such
// work to another goroutine.
type Hooks struct {
	Changed func(serverID int64, ch state.Change)
	Status  func(serverID int64, st Status)
	Notify  func(serverID int64, c state.NotifyCandidate)
}

// Files uploads the attachments of posts being sent (the api layer wires
// the attachment store). Its methods must not call back into the worker.
type Files interface {
	// Wait returns the server file ids of the attachments ids (in order)
	// once every one is uploaded; an error once one failed or is gone, or
	// once ctx ends. It does not time out by itself: an upload waits for
	// the server to be live and fails on its own stall timer.
	Wait(ctx context.Context, ids []string) ([]string, error)
	// Retry uploads the failed ones among ids again (the user asked).
	Retry(ids []string)
	// Release lets the attachments go — their post was confirmed,
	// discarded or lost: uploads are cancelled, spools deleted.
	Release(ids []string)
	// ReleaseComposer lets go of everything still staged in a (channel,
	// root) composer that no longer exists (its channel was left, taking
	// its held threads with it) — an upload already running is left to
	// finish or fail on its own.
	ReleaseComposer(srv int64, ch, root string)
}

type Config struct {
	Store       *store.Store
	HTTPClient  *http.Client
	Hooks       Hooks
	Files       Files // nil: posts cannot carry attachments
	Now         func() time.Time
	FlushEvery  time.Duration
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
	WakeCheck   time.Duration
	Fetchers    int
	StatusEvery time.Duration     // presence poll period; 0 → 1 min
	CallTimeout time.Duration     // bounds each request of an action run past its caller (reactions); 0 → 15 s
	WS          func(*ws.Options) // test seam (ping intervals)

	sinceLimit int // test seam: the server's since= cap; 0 → rest.SinceLimit
	// test seams: 0 → refreshTimeout / refreshRetryFirst
	refreshTimeout time.Duration
	refreshRetry   time.Duration
	reactBackoff   []time.Duration // test seam: nil → defaultReactBackoff
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.FlushEvery <= 0 {
		c.FlushEvery = 3 * time.Second
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 60 * time.Second
	}
	if c.WakeCheck <= 0 {
		c.WakeCheck = 5 * time.Second
	}
	if c.Fetchers <= 0 {
		c.Fetchers = 3
	}
	if c.StatusEvery <= 0 {
		c.StatusEvery = defaultStatusEvery
	}
	if len(c.reactBackoff) == 0 {
		c.reactBackoff = defaultReactBackoff
	}
	if c.CallTimeout <= 0 {
		c.CallTimeout = defaultCallTimeout
	}
	if c.refreshTimeout <= 0 {
		c.refreshTimeout = refreshTimeout
	}
	if c.refreshRetry <= 0 {
		c.refreshRetry = refreshRetryFirst
	}
	if c.sinceLimit <= 0 {
		c.sinceLimit = rest.SinceLimit
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
}

const (
	sinceMargin    = 2 * time.Minute
	retryFetchIn   = 10 * time.Second
	createTimeout  = 30 * time.Second
	metaDebounce   = 300 * time.Millisecond
	finalFlushTime = 5 * time.Second
	// resumeSettle: a server that refuses a resume answers with a hello
	// first thing; a resumed socket that stays quiet this long without one
	// was accepted (Mattermost sends nothing at all on a lossless resume).
	resumeSettle = 3 * time.Second
	// maxResumeFails: consecutive resumes that end before they are proven
	// (the server closes the socket on a connection_id it rejects) before
	// the worker gives up on the saved stream and reconnects fresh.
	maxResumeFails = 3
	// refreshTimeout bounds a metadata refresh as a whole: events are held
	// back while it runs, so a hung refresh is abandoned and redone later.
	refreshTimeout = 45 * time.Second
	// A timed-out refresh is retried after refreshRetryFirst, doubling up to
	// refreshRetryCap; a successful refresh resets it.
	refreshRetryFirst = 5 * time.Second
	refreshRetryCap   = 60 * time.Second
	// dialTimeout bounds the WebSocket handshake only; the established
	// connection lives as long as the session.
	dialTimeout = 30 * time.Second
	// defaultCallTimeout: Config.CallTimeout when unset (the api layer's
	// per-call bound).
	defaultCallTimeout = 15 * time.Second
)

var errNudged = errors.New("mmsync: reconnect requested")

// sessionExpired: only 401 means the token is dead; 403 is a per-resource
// permission refusal (rest classifies both as KindAuth).
func sessionExpired(err error) bool {
	var e *rest.Error
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

type Worker struct {
	cfg Config
	srv store.Server
	rc  *rest.Client
	st  *state.Server

	// Background work (actions, profile loads) runs under life, the
	// worker's lifetime ctx: created by NewWorker, cancelled when Run's ctx
	// is. Once Run is stopping, goBG refuses new work (stopping, under bgMu)
	// so bg.Add never races Run's final bg.Wait.
	life        context.Context
	cancelLife  context.CancelFunc
	bgMu        sync.Mutex
	stopping    bool
	status      atomic.Value // Status
	nudge       chan struct{}
	authFail    chan struct{}
	metaReq     chan struct{}
	metaDue     chan struct{} // debounced metaReq, served by the session's event loop
	statusDue   chan struct{} // a presence poll is wanted soon
	queue       *fetchQueue
	viewing     sync.Map    // channel id → in-flight view
	threadLoads sync.Map    // root id → in-flight thread load (loadThread)
	threadAgain sync.Map    // root id → a load was asked for while one ran
	threadReads sync.Map    // root id → in-flight server read (readThread)
	countsBusy  atomic.Bool // a reread of the thread mention totals runs
	countsAgain atomic.Bool // one was asked for while it ran
	reactMu     sync.Mutex
	reactPairs  map[string]*reactPair // post/emoji → our reaction being sent or waiting for a retry
	reactPoke   chan struct{}         // a pair started waiting: reactLoop re-arms its timer
	reactNow    chan struct{}         // live again: reactLoop retries every waiting pair
	emojiLoad   atomic.Bool           // custom emoji list read (or being read) by this worker
	missMu      sync.Mutex
	emojiMiss   map[string]time.Time // custom emoji names the server does not have
	usersMu     sync.Mutex
	bg          sync.WaitGroup
	live        liveMark

	// only touched by the Run goroutine
	resume ws.Resume
	booted bool
	// needResync: the stream was lost but the resync has not happened yet
	// (bootstrap failed, or a reset was seen while draining); the next
	// session must bootstrap even though it can resume the new stream.
	needResync  bool
	resumeFails int
	// followUp: the refresh in progress was requested by an unsettled
	// fetchMeta; a second unsettled read does not ask again (a busy server
	// would otherwise keep refreshing forever).
	followUp bool
	// refreshBackoff: delay before retrying the next timed-out refresh.
	refreshBackoff time.Duration

	// only touched by the flush loop: a delta SaveCache failed to write,
	// retried (merged with newer changes) by the next flush.
	unsavedPut []store.CacheEntry
	unsavedDel []store.CacheKey
}

func NewWorker(cfg Config, srv store.Server) *Worker {
	cfg.defaults()
	w := &Worker{
		cfg: cfg, srv: srv,
		rc:         rest.New(srv.URL, srv.Token, cfg.HTTPClient).WithLimiter(rest.NewLimiter()),
		st:         state.New(cfg.Now),
		nudge:      make(chan struct{}, 1),
		authFail:   make(chan struct{}, 1),
		metaReq:    make(chan struct{}, 1),
		metaDue:    make(chan struct{}, 1),
		statusDue:  make(chan struct{}, 1),
		queue:      newFetchQueue(),
		emojiMiss:  map[string]time.Time{},
		reactPairs: map[string]*reactPair{},
		reactPoke:  make(chan struct{}, 1),
		reactNow:   make(chan struct{}, 1),
	}
	w.life, w.cancelLife = context.WithCancel(context.Background())
	w.status.Store(StatusOff)
	return w
}

func (w *Worker) State() *state.Server { return w.st }
func (w *Worker) Status() Status       { return w.status.Load().(Status) }

func (w *Worker) setStatus(s Status) {
	if w.status.Swap(s) != s && w.cfg.Hooks.Status != nil {
		w.cfg.Hooks.Status(w.srv.ID, s)
	}
}

func (w *Worker) changed(c state.Change) {
	w.releaseFiles()
	if !c.Empty() && w.cfg.Hooks.Changed != nil {
		w.cfg.Hooks.Changed(w.srv.ID, c)
	}
}

// Nudge asks for an immediate reconnect (wake from sleep, network back).
func (w *Worker) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// CheckAuth sends a request error made through REST() outside the worker
// (media fetches) down the worker's re-auth path: a 401 ends the session
// and the server asks for a new sign-in.
func (w *Worker) CheckAuth(err error) {
	if sessionExpired(err) {
		w.signalAuth()
	}
}

func (w *Worker) signalAuth() {
	select {
	case w.authFail <- struct{}{}:
	default:
	}
}

// goBG runs fn in the background under the worker's lifetime ctx. Work
// submitted before Run starts is accepted (Run waits for it before its
// final flush); once Run is stopping it is refused and goBG reports false.
func (w *Worker) goBG(fn func(ctx context.Context)) bool {
	w.bgMu.Lock()
	if w.stopping {
		w.bgMu.Unlock()
		return false
	}
	w.bg.Add(1)
	w.bgMu.Unlock()
	go func() {
		defer w.bg.Done()
		fn(w.life)
	}()
	return true
}

// Run syncs the server until ctx is cancelled. Shutdown order: the session
// ends (connection closed, buffered events drained), the loops stop, new
// background work is refused and running work is cancelled and awaited,
// then the final snapshot flush — so it holds every change made so far.
// A Worker runs once; a worker that is never Run must not be given actions.
func (w *Worker) Run(ctx context.Context) {
	defer context.AfterFunc(ctx, w.cancelLife)()
	w.restore(ctx)
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){w.flushLoop, w.fetchLoop, w.metaLoop, w.wakeLoop, w.statusLoop, w.reactLoop} {
		wg.Add(1)
		go func() { defer wg.Done(); loop(ctx) }()
	}
	attempt := 0
	for ctx.Err() == nil {
		if attempt == 0 {
			w.setStatus(StatusConnecting)
		} else {
			w.setStatus(StatusReconnecting)
		}
		err := w.session(ctx, func() { attempt = 0 })
		if ctx.Err() != nil {
			break
		}
		if sessionExpired(err) {
			slog.Warn("session expired, sign-in needed", "srv", w.srv.ID, "err", err)
			w.setStatus(StatusNeedsReauth)
			<-ctx.Done()
			break
		}
		if errors.Is(err, errNudged) {
			attempt = 0
			continue
		}
		attempt++
		d := min(w.cfg.MinBackoff<<(attempt-1), w.cfg.MaxBackoff)
		if d <= 0 {
			d = w.cfg.MaxBackoff
		}
		slog.Info("server connection lost, retrying", "srv", w.srv.ID, "in", d, "err", err)
		w.setStatus(StatusReconnecting)
		select {
		case <-ctx.Done():
		case <-w.nudge:
		case <-time.After(d):
		}
	}
	wg.Wait()
	w.bgMu.Lock()
	w.stopping = true
	w.bgMu.Unlock()
	w.cancelLife()
	w.bg.Wait()
	// The thread cache is memory only: nothing of it outlives the worker.
	w.st.CloseThread()
	w.st.ResetThreads()
	// Pending posts live only in this worker's memory: their attachments
	// go with them.
	w.releaseFiles()
	if w.cfg.Files != nil {
		if ids := w.st.PendingAttachments(); len(ids) > 0 {
			w.cfg.Files.Release(ids)
		}
	}
	fctx, cancel := context.WithTimeout(context.Background(), finalFlushTime)
	w.flush(fctx)
	cancel()
	w.setStatus(StatusOff)
}

func (w *Worker) wsOptions() ws.Options {
	o := ws.Options{BaseURL: w.srv.URL, Token: w.srv.Token, HTTPClient: w.cfg.HTTPClient}
	if w.cfg.WS != nil {
		w.cfg.WS(&o)
	}
	return o
}

// session is one connection lifetime: validate the token, dial (resuming
// the previous stream if any), bootstrap when there is no stream to
// continue, then apply events until the stream ends.
//
// Continuity bookkeeping (w.live): the moment the previous stream ended is
// the start of the gap. It only moves forward once the new stream is proven
// continuous — a bootstrap completed on it, or the resume was accepted (an
// event in sequence, or resumeSettle without a hello). A refused resume
// (reset hello) therefore marks the windows stale from the real gap start.
func (w *Worker) session(ctx context.Context, onLive func()) (err error) {
	var refreshes sync.WaitGroup
	defer refreshes.Wait() // runs after cancel: an in-flight refresh ends with sctx
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	select { // drop stale signals from the previous session
	case <-w.authFail:
	default:
	}
	if _, err := w.rc.Me(sctx); err != nil {
		return err
	}
	dctx, dcancel := context.WithTimeout(sctx, dialTimeout)
	conn, err := ws.Dial(dctx, w.wsOptions(), w.resume)
	dcancel()
	if err != nil {
		return err
	}
	resumed := w.resume.ConnectionID != ""
	// A metadata refresh runs while events keep coming: they are held back
	// and applied after the refreshed metadata, under its guard.
	var held []ws.Event
	var refresh chan metaResult // non-nil while a refresh fetch is in flight
	defer func() {
		// Resume counts every event already placed in Events(): apply the
		// held and buffered ones before taking it, or they would be
		// skipped for good.
		conn.Close()
		for _, ev := range held {
			w.applyDrained(ev)
		}
		w.drain(conn.Events())
		w.resume = conn.Resume()
		now := w.cfg.Now().UnixMilli()
		if w.live.end(now) {
			w.st.SetLiveAt(now)
		} else if resumed && ctx.Err() == nil && !errors.Is(err, errNudged) && !sessionExpired(err) {
			w.resumeFails++
			if w.resumeFails >= maxResumeFails {
				slog.Info("server keeps refusing the resume, reconnecting fresh", "srv", w.srv.ID)
				w.resume, w.needResync, w.resumeFails = ws.Resume{}, true, 0
			}
		}
		if refresh != nil { // abandoned: done again once a session runs
			w.requestMeta()
		}
	}()
	if !w.booted || !resumed || w.needResync {
		if err := w.bootstrap(sctx, w.booted); err != nil {
			return err
		}
		w.proveLive()
	}
	w.setStatus(StatusLive)
	w.requestStatuses()
	w.retryReactionsNow()
	onLive()
	var settle <-chan time.Time
	if !w.live.proven() {
		settle = time.After(resumeSettle)
	}
	for {
		due := w.metaDue
		if refresh != nil {
			due = nil // one refresh at a time; a new request waits in metaDue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.nudge:
			return errNudged
		case <-w.authFail:
			return &rest.Error{Kind: rest.KindAuth, Status: http.StatusUnauthorized, Err: errors.New("request rejected with 401")}
		case <-settle:
			if refresh != nil || len(held) > 0 {
				// A hello may be among the held events: only the
				// socket being quiet with nothing held proves the resume.
				settle = time.After(resumeSettle)
				continue
			}
			w.proveLive() // no hello: the server accepted the resume
		case <-due:
			refresh = w.startRefresh(sctx, &refreshes)
		case r := <-refresh:
			refresh = nil
			if err := w.finishRefresh(r); err != nil {
				return err
			}
			evs := held
			held = nil
			for i, ev := range evs {
				if err := w.handle(sctx, ev); err != nil {
					held = evs[i+1:] // the cleanup applies the rest
					return err
				}
			}
			if len(conn.Events()) == 0 {
				w.st.ClearGuard()
			}
		case ev, ok := <-conn.Events():
			if !ok {
				return conn.Err()
			}
			if refresh != nil {
				held = append(held, ev)
				continue
			}
			if err := w.handle(sctx, ev); err != nil {
				return err
			}
			if len(conn.Events()) == 0 {
				w.st.ClearGuard()
			}
		}
	}
}

// handle applies one event of the live stream.
func (w *Worker) handle(ctx context.Context, ev ws.Event) error {
	if ev.Type == "hello" {
		if ev.Reset {
			slog.Info("event stream lost, resyncing", "srv", w.srv.ID)
			if err := w.bootstrap(ctx, true); err != nil {
				return err
			}
		}
		w.proveLive()
		return nil
	}
	w.apply(ev)
	w.proveLive() // Conn checked the sequence: the stream continues ours
	return nil
}

// proveLive records that the current stream is continuous with what we
// hold: from now on the live mark follows the clock.
func (w *Worker) proveLive() {
	if w.live.prove(w.cfg.Now().UnixMilli()) {
		w.resumeFails = 0
	}
}

// liveMark tracks up to when the event stream is known to be continuous.
// After a disconnect, at is where the gap starts; it moves forward again
// only once the new stream is proven. Shared by the Run goroutine and the
// flush loop.
type liveMark struct {
	mu     sync.Mutex
	isLive bool
	at     int64
}

// prove marks the stream continuous as of now; false if it already was.
func (m *liveMark) prove(now int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isLive {
		return false
	}
	m.isLive, m.at = true, max(m.at, now)
	return true
}

// seed sets the gap start before any stream (from the restored snapshot).
func (m *liveMark) seed(at int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at = max(m.at, at)
}

func (m *liveMark) proven() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isLive
}

// advance moves the mark to now if the stream is proven continuous.
func (m *liveMark) advance(now int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isLive {
		m.at = max(m.at, now)
	}
	return m.isLive
}

// end closes the stream: if it was proven, the gap starts now; otherwise it
// still starts where the last proven stream ended. Reports whether the
// stream was proven.
func (m *liveMark) end(now int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	was := m.isLive
	if was {
		m.at = max(m.at, now)
	}
	m.isLive = false
	return was
}

func (m *liveMark) gapStart() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.at
}

type metaResult struct {
	b        state.Bootstrap
	settled  bool
	timedOut bool
	err      error
}

// startRefresh fetches metadata in the background; the result goes back to
// the event loop, which holds events back until it lands.
func (w *Worker) startRefresh(ctx context.Context, wg *sync.WaitGroup) chan metaResult {
	out := make(chan metaResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rctx, cancel := context.WithTimeout(ctx, w.cfg.refreshTimeout)
		defer cancel()
		b, settled, err := w.fetchMeta(rctx)
		timedOut := err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(rctx.Err(), context.DeadlineExceeded))
		out <- metaResult{b: b, settled: settled, timedOut: timedOut, err: err}
	}()
	return out
}

// finishRefresh applies a refresh result on the event loop. The caller then
// applies the events held back meanwhile, under the fresh guard: posts the
// REST read already counted are not counted again, later ones are.
func (w *Worker) finishRefresh(r metaResult) error {
	if r.err != nil {
		if sessionExpired(r.err) {
			return r.err
		}
		slog.Warn("metadata refresh failed", "srv", w.srv.ID, "err", r.err, "timed_out", r.timedOut)
		if r.timedOut { // abandoned: the held events go on, the refresh is redone later
			w.retryRefresh()
		}
		return nil
	}
	w.refreshBackoff = 0
	crtChanged := w.st.Bootstrap(r.b)
	if crtChanged {
		w.st.ResetWindows()
		w.st.ResetThreads()
	}
	w.metaSettled(r.settled)
	w.replayOrphans()
	w.goBG(w.loadUsers)
	w.enqueueAll()
	w.reloadOpenThread()
	w.startEmojiLoad()
	c := state.Change{Sidebar: true, Badge: true}
	if crtChanged {
		c.Channels = []string{w.st.Active()}
		c.Threads = w.openThreads()
	}
	w.changed(c)
	return nil
}

// retryRefresh requests the refresh again after a backoff (refreshRetry,
// doubling up to refreshRetryCap), so a server too slow for the deadline is
// not hammered.
func (w *Worker) retryRefresh() {
	d := w.refreshBackoff
	if d <= 0 {
		d = w.cfg.refreshRetry
	}
	w.refreshBackoff = min(2*d, refreshRetryCap)
	w.goBG(func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
			w.requestMeta()
		}
	})
}

// metaSettled asks for one follow-up refresh after an unsettled read (see
// fetchMeta), at most one per original request.
func (w *Worker) metaSettled(settled bool) {
	if settled || w.followUp {
		w.followUp = false
		return
	}
	w.followUp = true
	w.requestMeta()
}

// replayOrphans applies posts that arrived for channels the last Bootstrap
// brought in (first message of a new DM, a channel we were added to).
func (w *Worker) replayOrphans() {
	for _, ev := range w.st.TakeOrphans() {
		w.apply(ev)
	}
}

// bootstrap reads all metadata. lost: the event stream was lost, so every
// window may miss posts since the gap start — needResync stays set until
// this succeeds, so a failure is retried by the next session.
func (w *Worker) bootstrap(ctx context.Context, lost bool) error {
	if lost {
		w.needResync = true
	}
	// Held before this bootstrap: from the snapshot or an earlier stream,
	// possibly stale — refreshed once below. gapStart is where the last
	// proven stream ended (seeded from the snapshot's live mark).
	known, since := w.st.KnownUserIDs(), w.live.gapStart()
	b, settled, err := w.fetchMeta(ctx)
	if err != nil {
		return err
	}
	if w.st.Bootstrap(b) {
		// CRT switched without a preferences_changed (admin config, or a
		// snapshot saved in the other mode): the windows hold the wrong
		// kind of posts — refetched below by enqueueAll (and the open
		// thread by reloadOpenThread).
		w.st.ResetWindows()
		w.st.ResetThreads()
	}
	w.metaSettled(settled)
	if lost {
		w.st.MarkStale(w.live.gapStart())
	}
	w.replayOrphans()
	w.booted, w.needResync = true, false
	w.loadUsers(ctx)
	w.goBG(func(ctx context.Context) { w.refreshUsers(ctx, known, since) })
	w.enqueueAll()
	w.reloadOpenThread() // stale since the gap, or reset
	w.startEmojiLoad()
	w.changed(state.Change{Sidebar: true, Badge: true, Channels: []string{w.st.Active()}, Threads: w.openThreads()})
	return nil
}

// fetchMeta reads channels and memberships first: events that arrive
// while a refresh is in flight are applied after it, and absolute ones
// (member updates) should predate these reads as little as possible.
//
// The two reads are not atomic: a post landing between them is in the
// member counters but not in the channel's last_post_at (the guard), so its
// event would count it again. Channels are read once more to detect that;
// settled=false asks for another refresh, which repairs the counters.
func (w *Worker) fetchMeta(ctx context.Context) (b state.Bootstrap, settled bool, err error) {
	if b.Channels, err = w.rc.MyChannels(ctx); err != nil {
		return b, false, err
	}
	if b.Members, err = w.rc.MyChannelMembers(ctx); err != nil {
		return b, false, err
	}
	again, err := w.rc.MyChannels(ctx)
	if err != nil {
		return b, false, err
	}
	settled = sameLastPosts(b.Channels, again)
	cfg, err := w.rc.ClientConfig(ctx)
	if err != nil {
		return b, false, err
	}
	b.Config = state.Config{CollapsedThreads: cfg.CollapsedThreads, TeammateNameDisplay: cfg.TeammateNameDisplay,
		LockTeammateNameDisplay: cfg.LockTeammateNameDisplay == "true",
		CustomEmoji:             cfg.EnableCustomEmoji == "true",
		MaxFileSize:             cfg.MaxFileSizeBytes(),
		EnableFileAttachments:   cfg.FileAttachmentsEnabled(),
	}
	if b.Me, err = w.rc.Me(ctx); err != nil {
		return b, false, err
	}
	if b.Prefs, err = w.rc.MyPreferences(ctx); err != nil {
		return b, false, err
	}
	if b.Teams, err = w.rc.MyTeams(ctx); err != nil {
		return b, false, err
	}
	if state.CRTEnabled(b.Config, b.Prefs) {
		// Events held meanwhile are not counted on top of these (the
		// guard, state/threadcounts.go). A failure keeps the totals held so
		// far — except a dead session or our own deadline, as for categories.
		if b.ThreadMentions, err = w.fetchThreadCounts(ctx, threadTotalsTeam(b.Teams, w.st.NavTeam())); err != nil {
			if sessionExpired(err) {
				return b, false, err
			}
			if ctx.Err() != nil {
				return b, false, ctx.Err()
			}
			slog.Warn("thread mentions unavailable, keeping the previous ones", "srv", w.srv.ID, "err", err)
			b.ThreadMentions, b.ThreadMentionsFailed = nil, true
		}
	}
	if b.Status, err = w.rc.MyStatus(ctx); err != nil {
		slog.Debug("status unavailable", "srv", w.srv.ID, "err", err)
		b.Status.Status = "online"
	}
	b.Categories = map[string]model.OrderedCategories{}
	for _, t := range b.Teams {
		cats, err := w.rc.Categories(ctx, t.ID)
		if err != nil {
			// A dead session or our own deadline/cancel fails the whole
			// read: a partial one would blank the categories not read.
			if sessionExpired(err) {
				return b, false, err
			}
			if ctx.Err() != nil {
				return b, false, ctx.Err()
			}
			slog.Warn("sidebar categories unavailable, keeping the previous ones", "srv", w.srv.ID, "team", t.ID, "err", err)
			b.CategoriesFailed = append(b.CategoriesFailed, t.ID)
			continue
		}
		b.Categories[t.ID] = cats
	}
	if ctx.Err() != nil {
		return b, false, ctx.Err()
	}
	return b, settled, nil
}

func sameLastPosts(a, b []model.Channel) bool {
	last := make(map[string]int64, len(a))
	for _, c := range a {
		last[c.ID] = c.LastPostAt
	}
	for _, c := range b {
		if at, ok := last[c.ID]; ok && at != c.LastPostAt {
			return false
		}
	}
	return true
}

// drain applies the events left in a closed connection's buffer, like the
// live loop does; a reset hello among them can only be recorded — the
// resync happens at the start of the next session.
func (w *Worker) drain(events <-chan ws.Event) {
	for ev := range events {
		w.applyDrained(ev)
	}
	w.st.ClearGuard()
}

func (w *Worker) applyDrained(ev ws.Event) {
	if ev.Type == "hello" {
		if ev.Reset {
			w.needResync = true
		}
		return
	}
	w.apply(ev)
}

func (w *Worker) apply(ev ws.Event) {
	eff := w.st.ApplyEvent(ev)
	w.changed(eff.Change)
	if eff.NeedMeta {
		w.requestMeta()
	}
	if len(eff.NeedUsers) > 0 {
		w.goBG(w.loadUsers)
	}
	if eff.ShowDM != nil {
		pref := *eff.ShowDM
		w.goBG(func(ctx context.Context) {
			if err := w.rc.SavePreferences(ctx, []model.Preference{pref}); err != nil {
				slog.Warn("could not save DM visibility", "srv", w.srv.ID, "err", err)
			}
		})
	}
	if eff.View != "" {
		w.view(eff.View)
	}
	if eff.ReadThread != "" {
		w.readThread(eff.ReadThread)
	}
	if eff.RereadThreadCounts {
		w.rereadThreadCounts()
	}
	if eff.Notify != nil && w.cfg.Hooks.Notify != nil {
		w.cfg.Hooks.Notify(w.srv.ID, *eff.Notify)
	}
	if eff.Resync {
		w.st.ResetWindows()
		w.st.ResetThreads()
		w.enqueueAll()
		w.reloadOpenThread()
		w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}, Threads: w.openThreads()})
	}
}

func (w *Worker) requestMeta() {
	select {
	case w.metaReq <- struct{}{}:
	default:
	}
}

// metaLoop asks for a channels/memberships/categories refresh after events
// that change them (joined a channel, new DM, categories edited elsewhere),
// debounced so a burst costs one refresh. The session's event loop performs
// it, so the result is applied in order with the events.
func (w *Worker) metaLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.metaReq:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(metaDebounce):
		}
		select {
		case <-w.metaReq:
		default:
		}
		select { // served by the next event loop that runs
		case w.metaDue <- struct{}{}:
		default:
		}
	}
}

func (w *Worker) loadUsers(ctx context.Context) {
	w.usersMu.Lock()
	defer w.usersMu.Unlock()
	ids := w.st.MissingUserIDs()
	if len(ids) == 0 {
		return
	}
	users, err := w.rc.UsersByIDs(ctx, ids)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("user profiles unavailable", "srv", w.srv.ID, "err", err)
		return
	}
	w.st.SetUsers(users)
	w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}, Threads: w.openThreads()})
}

// refreshUsers re-reads users held before a bootstrap, once per bootstrap
// and in the background: profiles restored from the snapshot (or held over
// a gap) are never missing, so a picture or name changed while we were not
// live would otherwise stay stale — the picture version is part of an
// immutable media URL — until a live user_updated. With the gap start
// (minus sinceMargin for clock skew) the server returns only the users
// updated meanwhile; without one every profile is re-read (chunks of 100).
func (w *Worker) refreshUsers(ctx context.Context, ids []string, since int64) {
	if len(ids) == 0 {
		return
	}
	if since > 0 {
		since = max(1, since-sinceMargin.Milliseconds())
	}
	w.usersMu.Lock()
	defer w.usersMu.Unlock()
	users, err := w.rc.UsersByIDsSince(ctx, ids, since)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("user profiles not refreshed", "srv", w.srv.ID, "err", err)
		return
	}
	if w.st.RefreshUsers(users) {
		w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}, Threads: w.openThreads()})
	}
}

func (w *Worker) restore(ctx context.Context) {
	entries, err := w.cfg.Store.LoadCache(ctx, w.srv.ID)
	if err != nil {
		slog.Warn("cache unreadable", "srv", w.srv.ID, "err", err)
		return
	}
	switch err := w.st.Restore(entries); {
	case err == nil:
		// Until a stream is proven, a gap starts where the snapshot's did.
		w.live.seed(w.st.LiveAt())
		w.changed(state.Change{Sidebar: true, Badge: true})
	case errors.Is(err, state.ErrNoSnapshot):
	default:
		slog.Warn("discarding incompatible cache snapshot", "srv", w.srv.ID, "err", err)
		if err := w.cfg.Store.ClearCache(ctx, w.srv.ID); err != nil {
			slog.Warn("cache clear failed", "srv", w.srv.ID, "err", err)
		}
	}
}

func (w *Worker) flushLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return // Run does the final flush once everything else stopped
		case <-t.C:
			// live/at is persisted only while the stream is proven
			// continuous: a cold start catches up from there.
			if now := w.cfg.Now().UnixMilli(); w.live.advance(now) {
				w.st.SetLiveAt(now)
			}
			w.flush(ctx)
		}
	}
}

// flush writes what changed since the last successful flush. TakeSnapshot
// hands out each delta once, so a delta that failed to save is kept and
// merged into the next one.
func (w *Worker) flush(ctx context.Context) {
	put, del := w.st.TakeSnapshot()
	put, del = mergeDelta(w.unsavedPut, w.unsavedDel, put, del)
	if err := w.cfg.Store.SaveCache(ctx, w.srv.ID, put, del); err != nil {
		slog.Warn("cache flush failed, will retry", "srv", w.srv.ID, "err", err)
		w.unsavedPut, w.unsavedDel = put, del
		return
	}
	w.unsavedPut, w.unsavedDel = nil, nil
}

// mergeDelta combines an older unsaved snapshot delta with a newer one:
// for each key the newer operation (put or delete) wins.
func mergeDelta(oldPut []store.CacheEntry, oldDel []store.CacheKey, newPut []store.CacheEntry, newDel []store.CacheKey) ([]store.CacheEntry, []store.CacheKey) {
	if len(oldPut) == 0 && len(oldDel) == 0 {
		return newPut, newDel
	}
	type op struct {
		put *store.CacheEntry // nil → delete
	}
	ops := map[store.CacheKey]op{}
	var order []store.CacheKey
	set := func(k store.CacheKey, o op) {
		if _, ok := ops[k]; !ok {
			order = append(order, k)
		}
		ops[k] = o
	}
	for i := range oldPut {
		set(store.CacheKey{Kind: oldPut[i].Kind, Key: oldPut[i].Key}, op{put: &oldPut[i]})
	}
	for _, k := range oldDel {
		set(k, op{})
	}
	for i := range newPut {
		set(store.CacheKey{Kind: newPut[i].Kind, Key: newPut[i].Key}, op{put: &newPut[i]})
	}
	for _, k := range newDel {
		set(k, op{})
	}
	var put []store.CacheEntry
	var del []store.CacheKey
	for _, k := range order {
		if o := ops[k]; o.put != nil {
			put = append(put, *o.put)
		} else {
			del = append(del, k)
		}
	}
	return put, del
}

func (w *Worker) wakeLoop(ctx context.Context) {
	watchWake(ctx, w.cfg.WakeCheck, func() {
		slog.Info("system woke up, reconnecting", "srv", w.srv.ID)
		w.Nudge()
	})
}
