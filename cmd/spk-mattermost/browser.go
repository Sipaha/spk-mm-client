package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/api/transport"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
)

func runBrowser(ctx context.Context, o browserOpts) error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	var fake *mmfake.Server
	if o.MMFake {
		fake = mmfake.Start(mmfake.Options{})
		defer fake.Close()
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
	h, _ := newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI)

	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", o.Port),
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	slog.Info("spk-mattermost browser mode", "url", "http://"+srv.Addr, "data", p.DataDir)
	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
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
