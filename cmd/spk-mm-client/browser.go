package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/api/transport"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/paths"
	"github.com/spk/spk-mm-client/internal/store"
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
		fake = mmfake.Start(mmfake.Options{ExtraChannels: o.FakeChannels})
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
	notes := &api.RecordingNotifier{} // browser mode has no OS notifications; e2e reads them via test-API
	svc.SetNotifier(notes)
	opened := &api.RecordingOpener{} // no system apps in browser mode; e2e reads them via test-API
	svc.SetFileOpener(opened.Open)
	revealed := recordReveals(svc)
	svc.EnableAttachments(p.TmpDir) // sweeps old spools in the background
	svc.ProtectDir(p.DataDir)       // the database, caches and spools are never attached
	// Before Start: a failed Start still stops the sweep and the uploads.
	closers = append(closers, svc.Close) // runs first: workers flush before the DB closes
	if err := svc.Start(ctx); err != nil {
		cleanup()
		return nil, nil, "", nil, fmt.Errorf("start sync: %w", err)
	}
	mc, err := media.New(media.Options{Dir: p.MediaDir, Origin: svc, Staged: svc})
	if err != nil {
		cleanup()
		return nil, nil, "", nil, fmt.Errorf("media cache: %w", err)
	}
	h, token := newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI, notes, mc, opened, revealed)

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
	slog.Info("spk-mm-client browser mode", "url", "http://"+srv.Addr, "data", p.DataDir)
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

const mediaCookie = "spk_media"

// mediaGuard admits the page's own requests (the cookie serveIndex set) and
// API-style callers with the bearer token.
func mediaGuard(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(mediaCookie)
		ok := err == nil && subtle.ConstantTimeCompare([]byte(c.Value), want) == 1
		ok = ok || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recordReveals makes "Show in folder" only record the file: browser mode
// has no file manager to ask; e2e reads the list via the test-API.
func recordReveals(svc *api.Service) *api.RecordingOpener {
	revealed := &api.RecordingOpener{}
	svc.SetRevealer(func(_ context.Context, path string) error { return revealed.Open(path) })
	return revealed
}

func newBrowserHandler(svc *api.Service, em *events.Emitter, dist fs.FS, fake *mmfake.Server, testAPI bool, notes *api.RecordingNotifier, mediaH http.Handler, opened, revealed *api.RecordingOpener) (http.Handler, string) {
	mux := http.NewServeMux()
	httpAPI := transport.NewHTTP(svc, em)
	token := httpAPI.AuthToken()
	mux.Handle("/api/", httpAPI)
	// The page's files as raw bodies: the same guards as /api/ (bearer —
	// never a query token on a POST —, Origin, and the loopback Host below).
	mux.Handle("POST /api/attachments/{srv}/{channel}", transport.AuthGuard(token, transport.OriginGuard(uploadHandler(svc))))
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
		writeJSON := func(w http.ResponseWriter, status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		withFake := func(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if fake == nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "no_fake_server"})
					return
				}
				fn(w, r)
			}
		}
		tm.HandleFunc("GET /api/_test/notifications", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, notes.List())
		})
		tm.HandleFunc("GET /api/_test/opened-files", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, opened.List())
		})
		tm.HandleFunc("GET /api/_test/revealed-files", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, revealed.List())
		})
		tm.HandleFunc("POST /api/_test/notification-click", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ServerID  int64  `json:"server_id"`
				ChannelID string `json:"channel_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			svc.NotificationClicked(in.ServerID, in.ChannelID)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		tm.HandleFunc("POST /api/_test/fake/post", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ChannelID string `json:"channel_id"`
				Username  string `json:"username"`
				Message   string `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			p := fake.PostAs(in.ChannelID, in.Username, in.Message)
			writeJSON(w, http.StatusOK, map[string]string{"id": p.ID})
		}))
		tm.HandleFunc("POST /api/_test/fake/drop", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Lose bool `json:"lose"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			fake.DropConnections(in.Lose)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
		tm.HandleFunc("POST /api/_test/fake/revoke", withFake(func(w http.ResponseWriter, _ *http.Request) {
			fake.RevokeAll()
			fake.DropConnections(true)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
		tm.HandleFunc("POST /api/_test/fake/status", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Username string `json:"username"`
				Status   string `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			fake.SetStatus(in.Username, in.Status)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
		tm.HandleFunc("POST /api/_test/fake/picture", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Username string `json:"username"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			writeJSON(w, http.StatusOK, map[string]int64{"at": fake.SetPicture(in.Username)})
		}))
		tm.HandleFunc("POST /api/_test/fake/throttle-file", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				BytesPerSec int `json:"bytes_per_sec"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			fake.SetFileThrottle(in.BytesPerSec)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
		tm.HandleFunc("POST /api/_test/fake/react", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ChannelID string `json:"channel_id"`
				Message   string `json:"message"`
				Username  string `json:"username"`
				Emoji     string `json:"emoji"`
				Remove    bool   `json:"remove"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			id := fake.FindPost(in.ChannelID, in.Message)
			if id == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "no_post"})
				return
			}
			if in.Remove {
				fake.UnreactAs(in.Username, id, in.Emoji)
			} else {
				fake.ReactAs(in.Username, id, in.Emoji)
			}
			writeJSON(w, http.StatusOK, map[string]string{"post_id": id})
		}))
		mux.Handle("/api/_test/", transport.AuthGuard(token, transport.OriginGuard(tm)))
		slog.Warn("test-api routes enabled at /api/_test/* — development only")
	}
	if mediaH != nil {
		mux.Handle("/media/", mediaGuard(token, mediaH))
	}
	mux.Handle("/", frontendHandler(token, dist))
	return transport.LoopbackHostGuard(mux), token
}
