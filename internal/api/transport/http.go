// Package transport exposes api.API to the UI: HTTP+SSE for browser mode
// (this file) and Wails bindings for desktop (wails.go).
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
)

const (
	ssePing         = 25 * time.Second
	sseWriteTimeout = 10 * time.Second
)

type HTTP struct {
	api       api.API
	events    *events.Emitter
	mux       *http.ServeMux
	authToken string
}

func NewHTTP(a api.API, em *events.Emitter) *HTTP {
	h := &HTTP{api: a, events: em, mux: http.NewServeMux(), authToken: newAuthToken()}
	h.routes()
	return h
}

func (h *HTTP) AuthToken() string { return h.authToken }

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") && !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	OriginGuard(h.mux).ServeHTTP(w, r)
}

func (h *HTTP) authorized(r *http.Request) bool {
	if bearerOK(r, h.authToken) {
		return true
	}
	// EventSource cannot set headers; the query token is accepted on SSE only.
	return r.URL.Path == "/api/events" && tokenEq(r.URL.Query().Get("token"), h.authToken)
}

func handle[Req any](fn func(ctx context.Context, req *Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, &api.CodedError{Code: api.CodeInternal, Detail: "bad request body: " + err.Error()})
				return
			}
		}
		out, err := fn(r.Context(), &req)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if out == nil {
			out = struct{}{}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	var ce *api.CodedError
	if !errors.As(err, &ce) {
		ce = &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": ce.Code, "detail": ce.Detail})
}

type idReq struct {
	ID int64 `json:"id"`
}

func (h *HTTP) routes() {
	h.mux.HandleFunc("POST /api/ListServers", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.ListServers(ctx)
	}))
	h.mux.HandleFunc("POST /api/AddServer", handle(func(ctx context.Context, r *struct {
		URL string `json:"url"`
	}) (any, error) {
		return h.api.AddServer(ctx, r.URL)
	}))
	h.mux.HandleFunc("POST /api/RemoveServer", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.RemoveServer(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/StartGitLabLogin", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.StartGitLabLogin(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/LoginWithPassword", handle(func(ctx context.Context, r *struct {
		ID       int64  `json:"id"`
		Login    string `json:"login"`
		Password string `json:"password"`
	}) (any, error) {
		return h.api.LoginWithPassword(ctx, r.ID, r.Login, r.Password)
	}))
	h.mux.HandleFunc("POST /api/Logout", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.Logout(ctx, r.ID)
	}))
	h.mux.HandleFunc("GET /api/events", h.serveEvents)
}

func (h *HTTP) serveEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, unsub := h.events.Subscribe()
	defer unsub()
	write := func(s string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		if _, err := fmt.Fprint(w, s); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write(": ok\n\n") {
		return
	}
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if !write(": ping\n\n") {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if !write("data: " + string(b) + "\n\n") {
				return
			}
		}
	}
}
