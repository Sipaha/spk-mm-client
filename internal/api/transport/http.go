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

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/events"
)

// MaxAPIBody caps an /api/ JSON body: far above the largest legitimate one
// (a slash command of 16383 runes, ≤ 64 KiB); browser uploads have their
// own route and limit (MaxFileSize).
const MaxAPIBody = 1 << 20

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
		r.Body = http.MaxBytesReader(w, r.Body, MaxAPIBody)
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

type chanReq struct {
	ID        int64  `json:"id"`
	ChannelID string `json:"channel_id"`
}

type postReq struct {
	ID     int64  `json:"id"`
	PostID string `json:"post_id"`
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
	h.mux.HandleFunc("POST /api/AppInfo", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.AppInfo(ctx)
	}))
	h.mux.HandleFunc("POST /api/GetLayout", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.GetLayout(ctx)
	}))
	h.mux.HandleFunc("POST /api/SetSidebarWidth", handle(func(ctx context.Context, r *struct {
		Width int `json:"width"`
	}) (any, error) {
		return nil, h.api.SetSidebarWidth(ctx, r.Width)
	}))
	h.mux.HandleFunc("POST /api/SetThreadWidth", handle(func(ctx context.Context, r *struct {
		Width int `json:"width"`
	}) (any, error) {
		return nil, h.api.SetThreadWidth(ctx, r.Width)
	}))
	h.mux.HandleFunc("POST /api/GetFormattingBarHidden", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.GetFormattingBarHidden(ctx)
	}))
	h.mux.HandleFunc("POST /api/SetFormattingBarHidden", handle(func(ctx context.Context, r *struct {
		Hidden bool `json:"hidden"`
	}) (any, error) {
		return nil, h.api.SetFormattingBarHidden(ctx, r.Hidden)
	}))
	h.mux.HandleFunc("POST /api/SelectServer", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.SelectServer(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/SetFocused", handle(func(ctx context.Context, r *struct {
		Focused bool `json:"focused"`
	}) (any, error) {
		return nil, h.api.SetFocused(ctx, r.Focused)
	}))
	h.mux.HandleFunc("POST /api/NetworkChanged", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return nil, h.api.NetworkChanged(ctx)
	}))
	h.mux.HandleFunc("POST /api/OpenURL", handle(func(ctx context.Context, r *struct {
		URL string `json:"url"`
	}) (any, error) {
		return nil, h.api.OpenURL(ctx, r.URL)
	}))
	h.mux.HandleFunc("POST /api/Sidebar", handle(func(ctx context.Context, r *struct {
		ID     int64  `json:"id"`
		TeamID string `json:"team_id"`
	}) (any, error) {
		return h.api.Sidebar(ctx, r.ID, r.TeamID)
	}))
	h.mux.HandleFunc("POST /api/OpenChannel", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return h.api.OpenChannel(ctx, r.ID, r.ChannelID)
	}))
	h.mux.HandleFunc("POST /api/GetChannel", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return h.api.GetChannel(ctx, r.ID, r.ChannelID)
	}))
	h.mux.HandleFunc("POST /api/LoadOlder", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return nil, h.api.LoadOlder(ctx, r.ID, r.ChannelID)
	}))
	// JumpToPost runs under the request's context: the UI aborts a stale
	// jump, which cancels its server calls.
	h.mux.HandleFunc("POST /api/JumpToPost", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		PostID    string `json:"post_id"`
	}) (any, error) {
		return h.api.JumpToPost(ctx, r.ID, r.ChannelID, r.PostID)
	}))
	h.mux.HandleFunc("POST /api/LoadNewer", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return nil, h.api.LoadNewer(ctx, r.ID, r.ChannelID)
	}))
	h.mux.HandleFunc("POST /api/RetryRevalidation", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return nil, h.api.RetryRevalidation(ctx, r.ID, r.ChannelID)
	}))
	type threadReq struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
	}
	h.mux.HandleFunc("POST /api/OpenThread", handle(func(ctx context.Context, r *threadReq) (any, error) {
		return h.api.OpenThread(ctx, r.ID, r.ChannelID, r.RootID)
	}))
	// OpenThreadAt runs under the request's context, like JumpToPost.
	h.mux.HandleFunc("POST /api/OpenThreadAt", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
		ReplyID   string `json:"reply_id"`
	}) (any, error) {
		return h.api.OpenThreadAt(ctx, r.ID, r.ChannelID, r.RootID, r.ReplyID)
	}))
	h.mux.HandleFunc("POST /api/LoadThreadFocus", handle(func(ctx context.Context, r *struct {
		ID     int64  `json:"id"`
		RootID string `json:"root_id"`
		Newer  bool   `json:"newer"`
	}) (any, error) {
		return nil, h.api.LoadThreadFocus(ctx, r.ID, r.RootID, r.Newer)
	}))
	h.mux.HandleFunc("POST /api/GetThread", handle(func(ctx context.Context, r *threadReq) (any, error) {
		return h.api.GetThread(ctx, r.ID, r.RootID)
	}))
	h.mux.HandleFunc("POST /api/CloseThread", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.CloseThread(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/LoadOlderReplies", handle(func(ctx context.Context, r *threadReq) (any, error) {
		return nil, h.api.LoadOlderReplies(ctx, r.ID, r.RootID)
	}))
	h.mux.HandleFunc("POST /api/SendPost", handle(func(ctx context.Context, r *struct {
		ID            int64    `json:"id"`
		ChannelID     string   `json:"channel_id"`
		Message       string   `json:"message"`
		AttachmentIDs []string `json:"attachment_ids"`
	}) (any, error) {
		return nil, h.api.SendPost(ctx, r.ID, r.ChannelID, r.Message, r.AttachmentIDs)
	}))
	h.mux.HandleFunc("POST /api/SendReply", handle(func(ctx context.Context, r *struct {
		ID            int64    `json:"id"`
		ChannelID     string   `json:"channel_id"`
		RootID        string   `json:"root_id"`
		Message       string   `json:"message"`
		AttachmentIDs []string `json:"attachment_ids"`
	}) (any, error) {
		return nil, h.api.SendReply(ctx, r.ID, r.ChannelID, r.RootID, r.Message, r.AttachmentIDs)
	}))
	h.mux.HandleFunc("POST /api/RetryPost", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		PendingID string `json:"pending_id"`
	}) (any, error) {
		return nil, h.api.RetryPost(ctx, r.ID, r.ChannelID, r.PendingID)
	}))
	h.mux.HandleFunc("POST /api/DiscardPost", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		PendingID string `json:"pending_id"`
	}) (any, error) {
		return nil, h.api.DiscardPost(ctx, r.ID, r.ChannelID, r.PendingID)
	}))
	h.mux.HandleFunc("POST /api/EditPost", handle(func(ctx context.Context, r *struct {
		ID      int64  `json:"id"`
		PostID  string `json:"post_id"`
		Message string `json:"message"`
	}) (any, error) {
		return nil, h.api.EditPost(ctx, r.ID, r.PostID, r.Message)
	}))
	h.mux.HandleFunc("POST /api/DeletePost", handle(func(ctx context.Context, r *postReq) (any, error) {
		return nil, h.api.DeletePost(ctx, r.ID, r.PostID)
	}))
	h.mux.HandleFunc("POST /api/MarkUnread", handle(func(ctx context.Context, r *postReq) (any, error) {
		return nil, h.api.MarkUnread(ctx, r.ID, r.PostID)
	}))
	h.mux.HandleFunc("POST /api/SetPostSaved", handle(func(ctx context.Context, r *struct {
		ID     int64  `json:"id"`
		PostID string `json:"post_id"`
		Saved  bool   `json:"saved"`
	}) (any, error) {
		return nil, h.api.SetPostSaved(ctx, r.ID, r.PostID, r.Saved)
	}))
	h.mux.HandleFunc("POST /api/SaveDraft", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		Text      string `json:"text"`
	}) (any, error) {
		return nil, h.api.SaveDraft(ctx, r.ID, r.ChannelID, r.Text)
	}))
	h.mux.HandleFunc("POST /api/SaveThreadDraft", handle(func(ctx context.Context, r *struct {
		ID     int64  `json:"id"`
		RootID string `json:"root_id"`
		Text   string `json:"text"`
	}) (any, error) {
		return nil, h.api.SaveThreadDraft(ctx, r.ID, r.RootID, r.Text)
	}))
	type fileReq struct {
		ID     int64  `json:"id"`
		FileID string `json:"file_id"`
	}
	h.mux.HandleFunc("POST /api/DownloadFile", handle(func(ctx context.Context, r *fileReq) (any, error) {
		return h.api.DownloadFile(ctx, r.ID, r.FileID)
	}))
	h.mux.HandleFunc("POST /api/OpenFile", handle(func(ctx context.Context, r *fileReq) (any, error) {
		return h.api.OpenFile(ctx, r.ID, r.FileID)
	}))
	type reactReq struct {
		ID     int64  `json:"id"`
		PostID string `json:"post_id"`
		Emoji  string `json:"emoji"`
	}
	h.mux.HandleFunc("POST /api/AddReaction", handle(func(ctx context.Context, r *reactReq) (any, error) {
		return nil, h.api.AddReaction(ctx, r.ID, r.PostID, r.Emoji)
	}))
	h.mux.HandleFunc("POST /api/RemoveReaction", handle(func(ctx context.Context, r *reactReq) (any, error) {
		return nil, h.api.RemoveReaction(ctx, r.ID, r.PostID, r.Emoji)
	}))
	h.mux.HandleFunc("POST /api/EmojiInfo", handle(func(ctx context.Context, r *idReq) (any, error) {
		return h.api.EmojiInfo(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/ReactionUsers", handle(func(ctx context.Context, r *reactReq) (any, error) {
		return h.api.ReactionUsers(ctx, r.ID, r.PostID, r.Emoji)
	}))
	h.mux.HandleFunc("POST /api/MediaStreamBase", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.MediaStreamBase(ctx)
	}))
	h.mux.HandleFunc("POST /api/Downloads", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.Downloads(ctx)
	}))
	h.mux.HandleFunc("POST /api/OpenDownload", handle(func(ctx context.Context, r *idReq) (any, error) {
		return h.api.OpenDownload(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/RevealDownload", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.RevealDownload(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/RemoveDownload", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.RemoveDownload(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/ClearDownloads", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return nil, h.api.ClearDownloads(ctx)
	}))
	type attachmentReq struct {
		ID           int64  `json:"id"`
		AttachmentID string `json:"attachment_id"`
	}
	h.mux.HandleFunc("POST /api/Attachments", handle(func(ctx context.Context, r *threadReq) (any, error) {
		return h.api.Attachments(ctx, r.ID, r.ChannelID, r.RootID)
	}))
	h.mux.HandleFunc("POST /api/RemoveAttachment", handle(func(ctx context.Context, r *attachmentReq) (any, error) {
		return nil, h.api.RemoveAttachment(ctx, r.ID, r.AttachmentID)
	}))
	h.mux.HandleFunc("POST /api/RetryAttachment", handle(func(ctx context.Context, r *attachmentReq) (any, error) {
		return nil, h.api.RetryAttachment(ctx, r.ID, r.AttachmentID)
	}))
	h.mux.HandleFunc("POST /api/AttachFromClipboard", handle(func(ctx context.Context, r *threadReq) (any, error) {
		return h.api.AttachFromClipboard(ctx, r.ID, r.ChannelID, r.RootID)
	}))
	h.mux.HandleFunc("POST /api/PickAttachments", handle(func(ctx context.Context, r *threadReq) (any, error) {
		return h.api.PickAttachments(ctx, r.ID, r.ChannelID, r.RootID)
	}))
	// Autocomplete runs under the request's context: the UI aborts a stale
	// request, which cancels its server call.
	h.mux.HandleFunc("POST /api/Autocomplete", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		Kind      string `json:"kind"`
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
		Prefix    string `json:"prefix"`
	}) (any, error) {
		return h.api.Autocomplete(ctx, r.ID, r.Kind, r.ChannelID, r.RootID, r.Prefix)
	}))
	h.mux.HandleFunc("POST /api/ExecuteCommand", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
		Command   string `json:"command"`
	}) (any, error) {
		return nil, h.api.ExecuteCommand(ctx, r.ID, r.ChannelID, r.RootID, r.Command)
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
