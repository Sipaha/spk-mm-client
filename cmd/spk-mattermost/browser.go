package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/api/transport"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
)

// shutdownTimeout bounds how long graceful shutdown waits for active
// connections (e.g. the SSE /api/events stream) to finish on their own
// before the server is force-closed. Without a bound, Shutdown blocks until
// every connection closes itself — with an SSE tab left open in the UI, that
// never happens, and Ctrl+C hangs the process forever.
const shutdownTimeout = 3 * time.Second

func runBrowser(ctx context.Context, o browserOpts) error {
	srv, cancelBase, _, cleanup, err := buildBrowserServer(ctx, o)
	if err != nil {
		return err
	}
	defer cleanup()
	return serveWithGracefulShutdown(ctx, srv, cancelBase)
}

// buildBrowserServer wires the store, optional fake Mattermost server, API
// service and HTTP handler for browser mode, and returns the *not yet
// listening* http.Server plus its per-run auth token. Split out from
// runBrowser so tests can obtain the token and drive the server lifecycle
// directly (e.g. to prove graceful shutdown does not hang with an open SSE
// connection) without duplicating the wiring.
func buildBrowserServer(ctx context.Context, o browserOpts) (srv *http.Server, cancelBase context.CancelFunc, token string, cleanup func(), err error) {
	p, err := paths.Resolve()
	if err != nil {
		return nil, nil, "", nil, err
	}
	if err := p.Ensure(); err != nil {
		return nil, nil, "", nil, err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("open db: %w", err)
	}
	closers := []func(){func() { _ = st.Close() }}
	cleanup = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	var fake *mmfake.Server
	if o.MMFake {
		fake = mmfake.Start(mmfake.Options{})
		closers = append(closers, fake.Close)
		slog.Warn("fake Mattermost server started (development only)", "url", fake.URL())
	}

	em := events.NewEmitter()
	// Browser mode has no OS browser to hand the SSO page to: the UI opens it
	// in a new tab on this event.
	open := func(u string) error {
		em.Emit(events.Event{Type: api.EventOpenExternal, Payload: map[string]any{"url": u}})
		return nil
	}
	svc := api.NewService(st, em, open, &http.Client{Timeout: 30 * time.Second})
	h, token := newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI)

	// Request contexts derive from baseCtx (via Server.BaseContext) instead
	// of the default context.Background(), so cancelBase can cancel in-flight
	// long-lived requests (the SSE stream) directly — Shutdown alone does not
	// touch active connections, it only waits for them.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	srv = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", o.Port),
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	slog.Info("spk-mattermost browser mode", "url", "http://"+srv.Addr, "data", p.DataDir)
	return srv, cancelBase, token, cleanup, nil
}

// serveWithGracefulShutdown runs srv until ctx is done, then shuts it down:
// it cancels cancelBase first so long-lived handlers (SSE /api/events) see
// their request context canceled and return promptly, gives remaining
// connections shutdownTimeout to finish, and force-closes anything left
// after that so Shutdown can never hang the process.
func serveWithGracefulShutdown(ctx context.Context, srv *http.Server, cancelBase context.CancelFunc) error {
	go func() {
		<-ctx.Done()
		cancelBase()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func newBrowserHandler(svc *api.Service, em *events.Emitter, dist fs.FS, fake *mmfake.Server, testAPI bool) (http.Handler, string) {
	mux := http.NewServeMux()
	httpAPI := transport.NewHTTP(svc, em)
	token := httpAPI.AuthToken()
	mux.Handle("/api/", httpAPI)
	if testAPI {
		tm := http.NewServeMux()
		tm.HandleFunc("POST /api/_test/deeplink", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				URL string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			w.Header().Set("Content-Type", "application/json")
			if err := svc.HandleDeepLink(r.Context(), in.URL); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				code := api.CodeInternal
				var ce *api.CodedError
				if errors.As(err, &ce) {
					code = ce.Code
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})
		tm.HandleFunc("GET /api/_test/fake-url", func(w http.ResponseWriter, _ *http.Request) {
			u := ""
			if fake != nil {
				u = fake.URL()
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"url": u})
		})
		mux.Handle("/api/_test/", transport.AuthGuard(token, transport.OriginGuard(tm)))
		slog.Warn("test-api routes enabled at /api/_test/* — development only")
	}
	mux.Handle("/", frontendHandler(token, dist))
	return mux, token
}
