package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/state"
)

type fakeAPI struct {
	api.API // unimplemented methods panic: tests call only what they set up
	added   string
	sent    []string
	reacted []string
	dl      []string
	saved   []string
	threads []string
}

func (f *fakeAPI) Downloads(context.Context) ([]api.DownloadView, error) {
	return []api.DownloadView{{ID: 7, Name: "a.pdf", State: "done", Exists: true, Openable: true}}, nil
}

func (f *fakeAPI) OpenDownload(_ context.Context, id int64) (bool, error) {
	f.dl = append(f.dl, fmt.Sprintf("open %d", id))
	return true, nil
}

func (f *fakeAPI) RevealDownload(_ context.Context, id int64) error {
	f.dl = append(f.dl, fmt.Sprintf("reveal %d", id))
	if id == 0 {
		return &api.CodedError{Code: api.CodeNoFile}
	}
	return nil
}

func (f *fakeAPI) RemoveDownload(_ context.Context, id int64) error {
	f.dl = append(f.dl, fmt.Sprintf("remove %d", id))
	return nil
}

func (f *fakeAPI) ClearDownloads(context.Context) error {
	f.dl = append(f.dl, "clear")
	return nil
}

func (f *fakeAPI) SendPost(_ context.Context, id int64, channelID, message string, attachmentIDs []string) error {
	f.sent = append(f.sent, fmt.Sprintf("%d/%s/%s/%v", id, channelID, message, attachmentIDs))
	return nil
}

func (f *fakeAPI) SendReply(_ context.Context, id int64, channelID, rootID, message string, attachmentIDs []string) error {
	f.sent = append(f.sent, fmt.Sprintf("%d/%s/%s/%s/%v", id, channelID, rootID, message, attachmentIDs))
	return nil
}

func (f *fakeAPI) SaveThreadDraft(_ context.Context, id int64, rootID, text string) error {
	f.dl = append(f.dl, fmt.Sprintf("draft %d/%s/%s", id, rootID, text))
	return nil
}

func (f *fakeAPI) AddReaction(_ context.Context, id int64, postID, emoji string) error {
	f.reacted = append(f.reacted, fmt.Sprintf("%d/%s/%s", id, postID, emoji))
	return nil
}

func (f *fakeAPI) RemoveReaction(_ context.Context, id int64, postID, emoji string) error {
	f.reacted = append(f.reacted, fmt.Sprintf("-%d/%s/%s", id, postID, emoji))
	return nil
}

func (f *fakeAPI) SetPostSaved(_ context.Context, id int64, postID string, saved bool) error {
	f.saved = append(f.saved, fmt.Sprintf("%d/%s/%v", id, postID, saved))
	return nil
}

func (f *fakeAPI) EmojiInfo(_ context.Context, id int64) (api.EmojiDTO, error) {
	return api.EmojiDTO{Recent: []string{fmt.Sprint(id), "+1"}, Custom: []string{"partyparrot"}, CustomEnabled: true}, nil
}

func (f *fakeAPI) ReactionUsers(_ context.Context, id int64, postID, emoji string) (api.ReactionUsersDTO, error) {
	f.reacted = append(f.reacted, fmt.Sprintf("who/%d/%s/%s", id, postID, emoji))
	return api.ReactionUsersDTO{Users: []api.Reactor{{ID: "u-bob", Name: "bob", Avatar: "3"}}, Unknown: 1}, nil
}

func (f *fakeAPI) MediaStreamBase(context.Context) (string, error) { return "/media", nil }

func (f *fakeAPI) DownloadFile(_ context.Context, id int64, fileID string) (api.SavedFile, error) {
	return api.SavedFile{Path: fmt.Sprintf("/dl/%d/%s", id, fileID)}, nil
}

func (f *fakeAPI) GetChannel(_ context.Context, _ int64, channelID string) (api.ChannelDTO, error) {
	return api.ChannelDTO{ID: channelID, Name: "Town", Posts: []state.PostView{}}, nil
}

func (f *fakeAPI) ListServers(context.Context) ([]api.ServerDTO, error) {
	return []api.ServerDTO{{ID: 1, Name: "A"}}, nil
}
func (f *fakeAPI) AddServer(_ context.Context, u string) (api.ServerDTO, error) {
	f.added = u
	if u == "bad" {
		return api.ServerDTO{}, &api.CodedError{Code: api.CodeInvalidURL, Detail: "nope"}
	}
	return api.ServerDTO{ID: 2, URL: u}, nil
}
func (f *fakeAPI) RemoveServer(context.Context, int64) error     { return nil }
func (f *fakeAPI) StartGitLabLogin(context.Context, int64) error { return nil }
func (f *fakeAPI) LoginWithPassword(context.Context, int64, string, string) (api.ServerDTO, error) {
	return api.ServerDTO{}, nil
}
func (f *fakeAPI) Logout(context.Context, int64) error { return nil }

func call(t *testing.T, h *HTTP, srvURL, method, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/api/"+method, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.AuthToken())
	req.Header.Set("Origin", srvURL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestPostRoutesToAPI(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "AddServer", `{"url":"https://mm"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "https://mm", f.added)

	resp = call(t, h, ts.URL, "ListServers", `{}`)
	var list []api.ServerDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	assert.Equal(t, "A", list[0].Name)
}

func TestCodedErrorBecomes400WithCode(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp := call(t, h, ts.URL, "AddServer", `{"url":"bad"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "invalid_url", body["code"])
}

func TestMissingTokenIs401(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/ListServers", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

// TestWrongTokenIs401 locks in that a bearer token that doesn't match the
// server's authToken is rejected, not just a missing one (TestMissingTokenIs401
// covers the missing case).
func TestWrongTokenIs401(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ListServers", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

// TestQueryTokenOnlyAcceptedOnEventsRoute locks in that the ?token= query
// fallback (needed because EventSource cannot set headers) is honored ONLY
// on /api/events; any other /api/* route must reject a bare, even valid,
// query token and demand the Authorization header instead.
func TestQueryTokenOnlyAcceptedOnEventsRoute(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ListServers?token="+h.AuthToken(), strings.NewReader(`{}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestSSEDeliversEvents(t *testing.T) {
	em := events.NewEmitter()
	h := NewHTTP(&fakeAPI{}, em)
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events?token="+h.AuthToken(), nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n') // ": ok" preamble — subscription is live
	require.NoError(t, err)
	assert.Equal(t, ": ok\n", line)

	em.Emit(events.Event{Type: api.EventServersChanged})
	for {
		line, err = r.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			var ev events.Event
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev))
			assert.Equal(t, api.EventServersChanged, ev.Type)
			return
		}
	}
}

func TestChatRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "SendPost", `{"id":3,"channel_id":"c1","message":"hi"}`)
	assert.Equal(t, 200, resp.StatusCode)
	resp = call(t, h, ts.URL, "SendPost", `{"id":3,"channel_id":"c1","message":"","attachment_ids":["a1","a2"]}`)
	assert.Equal(t, 200, resp.StatusCode)
	resp = call(t, h, ts.URL, "SendReply", `{"id":3,"channel_id":"c1","root_id":"r1","message":"re"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"3/c1/hi/[]", "3/c1//[a1 a2]", "3/c1/r1/re/[]"}, f.sent)

	resp = call(t, h, ts.URL, "SaveThreadDraft", `{"id":3,"root_id":"r1","text":"draft text"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"draft 3/r1/draft text"}, f.dl)

	resp = call(t, h, ts.URL, "GetChannel", `{"id":3,"channel_id":"c1"}`)
	var ch map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ch))
	assert.Equal(t, "c1", ch["id"])
	assert.Equal(t, []any{}, ch["posts"])

	resp = call(t, h, ts.URL, "DownloadFile", `{"id":3,"file_id":"f1"}`)
	var saved api.SavedFile
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&saved))
	assert.Equal(t, "/dl/3/f1", saved.Path)

	resp = call(t, h, ts.URL, "AddReaction", `{"id":3,"post_id":"p1","emoji":"+1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"3/p1/+1"}, f.reacted)

	resp = call(t, h, ts.URL, "RemoveReaction", `{"id":3,"post_id":"p1","emoji":"+1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"3/p1/+1", "-3/p1/+1"}, f.reacted)

	resp = call(t, h, ts.URL, "SetPostSaved", `{"id":3,"post_id":"p1","saved":true}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"3/p1/true"}, f.saved)

	resp = call(t, h, ts.URL, "EmojiInfo", `{"id":3}`)
	assert.Equal(t, 200, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"recent":["3","+1"],"custom":["partyparrot"],"custom_enabled":true}`, string(raw))

	resp = call(t, h, ts.URL, "ReactionUsers", `{"id":3,"post_id":"p1","emoji":"+1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	raw, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"users":[{"id":"u-bob","name":"bob","avatar":"3"}],"unknown":1}`, string(raw))
	assert.Equal(t, []string{"3/p1/+1", "-3/p1/+1", "who/3/p1/+1"}, f.reacted)

	resp = call(t, h, ts.URL, "MediaStreamBase", `{}`)
	assert.Equal(t, 200, resp.StatusCode)
	raw, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `"/media"`, string(raw))
}

func TestDownloadRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "Downloads", `{}`)
	assert.Equal(t, 200, resp.StatusCode)
	var list []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list, 1)
	assert.Equal(t, float64(7), list[0]["id"])
	assert.Equal(t, true, list[0]["exists"])
	assert.Equal(t, true, list[0]["openable"])

	resp = call(t, h, ts.URL, "OpenDownload", `{"id":7}`)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `true`, string(raw))
	assert.Equal(t, 200, call(t, h, ts.URL, "RevealDownload", `{"id":7}`).StatusCode)
	resp = call(t, h, ts.URL, "RevealDownload", `{"id":0}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "no_file", body["code"])
	assert.Equal(t, 200, call(t, h, ts.URL, "RemoveDownload", `{"id":7}`).StatusCode)
	assert.Equal(t, 200, call(t, h, ts.URL, "ClearDownloads", `{}`).StatusCode)
	assert.Equal(t, []string{"open 7", "reveal 7", "reveal 0", "remove 7", "clear"}, f.dl)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ClearDownloads", strings.NewReader(`{}`))
	req.Header.Set("Origin", ts.URL)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode, "behind the same guard")
}

func (f *fakeAPI) Attachments(_ context.Context, id int64, channelID, rootID string) ([]api.AttachmentView, error) {
	f.dl = append(f.dl, fmt.Sprintf("list %d/%s/%s", id, channelID, rootID))
	return []api.AttachmentView{{ID: "a1", Name: "x.png", Size: 3, Mime: "image/png", State: "uploading", Sent: 1, FileID: "secret"}}, nil
}

func (f *fakeAPI) RemoveAttachment(_ context.Context, id int64, attachmentID string) error {
	f.dl = append(f.dl, fmt.Sprintf("remove %d/%s", id, attachmentID))
	return nil
}

func (f *fakeAPI) RetryAttachment(_ context.Context, id int64, attachmentID string) error {
	f.dl = append(f.dl, fmt.Sprintf("retry %d/%s", id, attachmentID))
	return &api.CodedError{Code: api.CodeSessionExpired}
}

func TestAttachmentRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "Attachments", `{"id":3,"channel_id":"c1","root_id":"r1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"id":"a1","name":"x.png","size":3,"mime":"image/png","state":"uploading","sent":1,"error":""}]`, string(raw))
	assert.Equal(t, 200, call(t, h, ts.URL, "RemoveAttachment", `{"id":3,"attachment_id":"a1"}`).StatusCode)
	resp = call(t, h, ts.URL, "RetryAttachment", `{"id":3,"attachment_id":"a1"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "session_expired", body["code"])
	assert.Equal(t, []string{"list 3/c1/r1", "remove 3/a1", "retry 3/a1"}, f.dl)
}

func (f *fakeAPI) AttachFromClipboard(_ context.Context, id int64, channelID, rootID string) (int, error) {
	f.dl = append(f.dl, fmt.Sprintf("paste %d/%s/%s", id, channelID, rootID))
	return 2, nil
}

func (f *fakeAPI) PickAttachments(_ context.Context, id int64, channelID, rootID string) (int, error) {
	f.dl = append(f.dl, fmt.Sprintf("pick %d/%s/%s", id, channelID, rootID))
	return 0, &api.CodedError{Code: api.CodeUnsupported}
}

func TestAttachmentSourceRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "AttachFromClipboard", `{"id":3,"channel_id":"c1","root_id":"r1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	var n int
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&n))
	assert.Equal(t, 2, n)
	resp = call(t, h, ts.URL, "PickAttachments", `{"id":3,"channel_id":"c1"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "unsupported", body["code"])
	assert.Equal(t, []string{"paste 3/c1/r1", "pick 3/c1/"}, f.dl)
}

func (f *fakeAPI) OpenThread(_ context.Context, id int64, channelID, rootID string) (api.ThreadDTO, error) {
	f.threads = append(f.threads, fmt.Sprintf("open %d/%s/%s", id, channelID, rootID))
	return api.ThreadDTO{RootID: rootID, ChannelID: channelID, Posts: []state.PostView{}}, nil
}

func (f *fakeAPI) GetThread(_ context.Context, id int64, rootID string) (api.ThreadDTO, error) {
	f.threads = append(f.threads, fmt.Sprintf("get %d/%s", id, rootID))
	if rootID == "nope" {
		return api.ThreadDTO{}, &api.CodedError{Code: api.CodeNoPost}
	}
	return api.ThreadDTO{RootID: rootID, Posts: []state.PostView{}}, nil
}

func (f *fakeAPI) CloseThread(_ context.Context, id int64) error {
	f.threads = append(f.threads, fmt.Sprintf("close %d", id))
	return nil
}

func (f *fakeAPI) LoadOlderReplies(_ context.Context, id int64, rootID string) error {
	f.threads = append(f.threads, fmt.Sprintf("older %d/%s", id, rootID))
	return nil
}

func TestThreadRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "OpenThread", `{"id":3,"channel_id":"c1","root_id":"r1"}`)
	require.Equal(t, 200, resp.StatusCode)
	var v map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	assert.Equal(t, "r1", v["root_id"])
	assert.Equal(t, "c1", v["channel_id"])
	assert.Equal(t, []any{}, v["posts"])
	for _, k := range []string{"has_more", "capped", "loaded", "syncing", "root_deleted", "error", "draft", "me_id", "crt", "new_since", "gap_after", "channel_name", "team_name"} {
		assert.Contains(t, v, k)
	}

	resp = call(t, h, ts.URL, "GetThread", `{"id":3,"root_id":"r1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	resp = call(t, h, ts.URL, "GetThread", `{"id":3,"root_id":"nope"}`)
	assert.Equal(t, 400, resp.StatusCode)
	resp = call(t, h, ts.URL, "LoadOlderReplies", `{"id":3,"root_id":"r1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	resp = call(t, h, ts.URL, "CloseThread", `{"id":3}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"open 3/c1/r1", "get 3/r1", "get 3/nope", "older 3/r1", "close 3"}, f.threads)
}

type acAPI struct {
	api.API
	calls   []string
	blocked chan struct{} // Autocomplete waits for its ctx to end when set
	ended   chan error
}

func (f *acAPI) Autocomplete(ctx context.Context, id int64, kind, channelID, rootID, prefix string) (api.AutocompleteDTO, error) {
	f.calls = append(f.calls, fmt.Sprintf("ac %d/%s/%s/%s/%s", id, kind, channelID, rootID, prefix))
	if f.blocked != nil {
		close(f.blocked)
		<-ctx.Done()
		f.ended <- ctx.Err()
		return api.AutocompleteDTO{}, ctx.Err()
	}
	return api.AutocompleteDTO{Users: []api.ACUser{{ID: "u1", Username: "bob"}}}, nil
}

func (f *acAPI) ExecuteCommand(_ context.Context, id int64, channelID, rootID, command string) error {
	f.calls = append(f.calls, fmt.Sprintf("exec %d/%s/%s/%s", id, channelID, rootID, command))
	if command == "/nope" {
		return &api.CodedError{Code: api.CodeCommandNotFound}
	}
	return nil
}

func TestAutocompleteAndCommandRoutes(t *testing.T) {
	f := &acAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "Autocomplete", `{"id":3,"kind":"users","channel_id":"c1","root_id":"r1","prefix":"bo"}`)
	require.Equal(t, 200, resp.StatusCode)
	var v struct {
		Users []api.ACUser `json:"users"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	assert.Equal(t, "bob", v.Users[0].Username)
	assert.Equal(t, 200, call(t, h, ts.URL, "ExecuteCommand", `{"id":3,"channel_id":"c1","root_id":"r1","command":"/echo hi"}`).StatusCode)
	resp = call(t, h, ts.URL, "ExecuteCommand", `{"id":3,"channel_id":"c1","command":"/nope"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var e map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	assert.Equal(t, "command_not_found", e["code"])
	assert.Equal(t, []string{"ac 3/users/c1/r1/bo", "exec 3/c1/r1//echo hi", "exec 3/c1///nope"}, f.calls)
}

// An aborted fetch (the UI dropped a stale autocomplete request) cancels
// the request context the API method runs under.
func TestAbortedAutocompleteCancelsItsContext(t *testing.T) {
	f := &acAPI{blocked: make(chan struct{}), ended: make(chan error, 1)}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/Autocomplete",
		strings.NewReader(`{"id":3,"kind":"users","channel_id":"c1","prefix":"b"}`))
	req.Header.Set("Authorization", "Bearer "+h.AuthToken())
	req.Header.Set("Origin", ts.URL)
	go func() {
		<-f.blocked
		cancel()
	}()
	_, err := http.DefaultClient.Do(req)
	require.Error(t, err)
	select {
	case err := <-f.ended:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the API call never saw the abort")
	}
}
