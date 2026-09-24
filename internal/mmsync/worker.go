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

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mm/ws"
	"github.com/spk/spk-mattermost/internal/state"
	"github.com/spk/spk-mattermost/internal/store"
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

type Hooks struct {
	Changed func(serverID int64, ch state.Change)
	Status  func(serverID int64, st Status)
	Notify  func(serverID int64, c state.NotifyCandidate)
}

type Config struct {
	Store      *store.Store
	HTTPClient *http.Client
	Hooks      Hooks
	Now        func() time.Time
	FlushEvery time.Duration
	MinBackoff time.Duration
	MaxBackoff time.Duration
	WakeCheck  time.Duration
	Fetchers   int
	WS         func(*ws.Options) // test seam (ping intervals)

	sinceLimit int // test seam: the server's since= cap; 0 → rest.SinceLimit
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
	// dialTimeout bounds the WebSocket handshake only; the established
	// connection lives as long as the session.
	dialTimeout = 30 * time.Second
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

	runCtx   atomic.Value // context.Context of Run; background work derives from it
	status   atomic.Value // Status
	nudge    chan struct{}
	authFail chan struct{}
	metaReq  chan struct{}
	queue    *fetchQueue
	viewing  sync.Map // channel id → in-flight view
	usersMu  sync.Mutex
	bg       sync.WaitGroup
	lastLive atomic.Int64

	// only touched by the Run goroutine
	resume ws.Resume
	booted bool
	// needResync: a hello reset the stream but the resync has not happened
	// yet (bootstrap failed, or the reset was seen while draining); the
	// next session must bootstrap even though it can resume the new stream.
	needResync bool

	// only touched by the flush loop: a delta SaveCache failed to write,
	// retried (merged with newer changes) by the next flush.
	unsavedPut []store.CacheEntry
	unsavedDel []store.CacheKey
}

func NewWorker(cfg Config, srv store.Server) *Worker {
	cfg.defaults()
	w := &Worker{
		cfg: cfg, srv: srv,
		rc:       rest.New(srv.URL, srv.Token, cfg.HTTPClient).WithLimiter(rest.NewLimiter()),
		st:       state.New(cfg.Now),
		nudge:    make(chan struct{}, 1),
		authFail: make(chan struct{}, 1),
		metaReq:  make(chan struct{}, 1),
		queue:    newFetchQueue(),
	}
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

func (w *Worker) signalAuth() {
	select {
	case w.authFail <- struct{}{}:
	default:
	}
}

// goBG runs fn in the background under Run's context (actions may arrive
// from UI goroutines before Run has started; they then get Background).
func (w *Worker) goBG(fn func(ctx context.Context)) {
	ctx, _ := w.runCtx.Load().(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}
	w.bg.Add(1)
	go func() {
		defer w.bg.Done()
		fn(ctx)
	}()
}

func (w *Worker) Run(ctx context.Context) {
	w.runCtx.Store(ctx)
	w.restore(ctx)
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){w.flushLoop, w.fetchLoop, w.metaLoop, w.wakeLoop} {
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
	w.bg.Wait()
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
func (w *Worker) session(ctx context.Context, onLive func()) error {
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
	defer func() {
		// Resume counts every event already placed in Events(): apply the
		// buffered ones before taking it, or they would be skipped for good.
		conn.Close()
		w.drain(conn.Events())
		w.resume = conn.Resume()
		if w.Status() == StatusLive {
			w.lastLive.Store(w.cfg.Now().UnixMilli())
		}
	}()
	if !w.booted || w.resume.ConnectionID == "" || w.needResync {
		if err := w.bootstrap(sctx, w.booted); err != nil {
			return err
		}
	}
	w.setStatus(StatusLive)
	w.lastLive.Store(w.cfg.Now().UnixMilli())
	onLive()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.nudge:
			return errNudged
		case <-w.authFail:
			return &rest.Error{Kind: rest.KindAuth, Status: http.StatusUnauthorized, Err: errors.New("request rejected with 401")}
		case ev, ok := <-conn.Events():
			if !ok {
				return conn.Err()
			}
			if ev.Type == "hello" {
				if ev.Reset {
					slog.Info("event stream lost, resyncing", "srv", w.srv.ID)
					w.needResync = true
					if err := w.bootstrap(sctx, true); err != nil {
						return err
					}
				}
				continue
			}
			w.apply(ev)
			if len(conn.Events()) == 0 {
				w.st.ClearGuard()
			}
		}
	}
}

func (w *Worker) bootstrap(ctx context.Context, lost bool) error {
	b, err := w.fetchMeta(ctx)
	if err != nil {
		return err
	}
	w.st.Bootstrap(b)
	if lost {
		w.st.MarkStale(w.lastLive.Load())
	}
	w.booted, w.needResync = true, false
	w.loadUsers(ctx)
	w.enqueueAll()
	w.changed(state.Change{Sidebar: true, Badge: true, Channels: []string{w.st.Active()}})
	return nil
}

func (w *Worker) fetchMeta(ctx context.Context) (state.Bootstrap, error) {
	var b state.Bootstrap
	cfg, err := w.rc.ClientConfig(ctx)
	if err != nil {
		return b, err
	}
	b.Config = state.Config{CollapsedThreads: cfg.CollapsedThreads, TeammateNameDisplay: cfg.TeammateNameDisplay,
		LockTeammateNameDisplay: cfg.LockTeammateNameDisplay == "true"}
	if b.Me, err = w.rc.Me(ctx); err != nil {
		return b, err
	}
	if b.Prefs, err = w.rc.MyPreferences(ctx); err != nil {
		return b, err
	}
	if b.Teams, err = w.rc.MyTeams(ctx); err != nil {
		return b, err
	}
	if b.Channels, err = w.rc.MyChannels(ctx); err != nil {
		return b, err
	}
	if b.Members, err = w.rc.MyChannelMembers(ctx); err != nil {
		return b, err
	}
	if b.Status, err = w.rc.MyStatus(ctx); err != nil {
		slog.Debug("status unavailable", "srv", w.srv.ID, "err", err)
		b.Status.Status = "online"
	}
	b.Categories = map[string]model.OrderedCategories{}
	for _, t := range b.Teams {
		cats, err := w.rc.Categories(ctx, t.ID)
		if err != nil {
			if sessionExpired(err) {
				return b, err
			}
			slog.Warn("sidebar categories unavailable", "srv", w.srv.ID, "team", t.ID, "err", err)
			continue
		}
		b.Categories[t.ID] = cats
	}
	return b, nil
}

// drain applies the events left in a closed connection's buffer, like the
// live loop does; a reset hello among them can only be recorded — the
// resync happens at the start of the next session.
func (w *Worker) drain(events <-chan ws.Event) {
	for ev := range events {
		if ev.Type == "hello" {
			if ev.Reset {
				w.needResync = true
			}
			continue
		}
		w.apply(ev)
	}
	w.st.ClearGuard()
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
	if eff.Notify != nil && w.cfg.Hooks.Notify != nil {
		w.cfg.Hooks.Notify(w.srv.ID, *eff.Notify)
	}
	if eff.Resync {
		w.st.ResetWindows()
		w.enqueueAll()
		w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}})
	}
}

func (w *Worker) requestMeta() {
	select {
	case w.metaReq <- struct{}{}:
	default:
	}
}

// metaLoop refreshes channels/memberships/categories after events that
// change them (joined a channel, new DM, categories edited elsewhere),
// debounced so a burst costs one refresh.
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
		b, err := w.fetchMeta(ctx)
		if err != nil {
			if sessionExpired(err) {
				w.signalAuth()
			}
			slog.Warn("metadata refresh failed", "srv", w.srv.ID, "err", err)
			continue
		}
		w.st.Bootstrap(b)
		w.loadUsers(ctx)
		w.enqueueAll()
		w.changed(state.Change{Sidebar: true, Badge: true})
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
	w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}})
}

func (w *Worker) restore(ctx context.Context) {
	entries, err := w.cfg.Store.LoadCache(ctx, w.srv.ID)
	if err != nil {
		slog.Warn("cache unreadable", "srv", w.srv.ID, "err", err)
		return
	}
	switch err := w.st.Restore(entries); {
	case err == nil:
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
			fctx, cancel := context.WithTimeout(context.Background(), finalFlushTime)
			w.flush(fctx)
			cancel()
			return
		case <-t.C:
			if w.Status() == StatusLive {
				now := w.cfg.Now().UnixMilli()
				w.lastLive.Store(now)
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
