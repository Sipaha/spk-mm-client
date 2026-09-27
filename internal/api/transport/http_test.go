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

func (f *fakeAPI) AddReaction(_ context.Context, id int64, postID, emoji string) error {
	f.reacted = append(f.reacted, fmt.Sprintf("%d/%s/%s", id, postID, emoji))
	return nil
}

func (f *fakeAPI) RemoveReaction(_ context.Context, id int64, postID, emoji string) error {
	f.reacted = append(f.reacted, fmt.Sprintf("-%d/%s/%s", id, postID, emoji))
	return nil
}

func (f *fakeAPI) EmojiInfo(_ context.Context, id int64) (api.EmojiDTO, error) {
	return api.EmojiDTO{Recent: []string{fmt.Sprint(id), "+1"}, Custom: []string{"partyparrot"}, CustomEnabled: true}, nil
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
	assert.Equal(t, []string{"3/c1/hi/[]", "3/c1//[a1 a2]"}, f.sent)

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

	resp = call(t, h, ts.URL, "EmojiInfo", `{"id":3}`)
	assert.Equal(t, 200, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"recent":["3","+1"],"custom":["partyparrot"],"custom_enabled":true}`, string(raw))

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

func (f *fakeAPI) Attachments(_ context.Context, id int64, channelID string) ([]api.AttachmentView, error) {
	f.dl = append(f.dl, fmt.Sprintf("list %d/%s", id, channelID))
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

	resp := call(t, h, ts.URL, "Attachments", `{"id":3,"channel_id":"c1"}`)
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
	assert.Equal(t, []string{"list 3/c1", "remove 3/a1", "retry 3/a1"}, f.dl)
}

func (f *fakeAPI) AttachFromClipboard(_ context.Context, id int64, channelID string) (int, error) {
	f.dl = append(f.dl, fmt.Sprintf("paste %d/%s", id, channelID))
	return 2, nil
}

func (f *fakeAPI) PickAttachments(_ context.Context, id int64, channelID string) (int, error) {
	f.dl = append(f.dl, fmt.Sprintf("pick %d/%s", id, channelID))
	return 0, &api.CodedError{Code: api.CodeUnsupported}
}

func TestAttachmentSourceRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "AttachFromClipboard", `{"id":3,"channel_id":"c1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	var n int
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&n))
	assert.Equal(t, 2, n)
	resp = call(t, h, ts.URL, "PickAttachments", `{"id":3,"channel_id":"c1"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "unsupported", body["code"])
	assert.Equal(t, []string{"paste 3/c1", "pick 3/c1"}, f.dl)
}
