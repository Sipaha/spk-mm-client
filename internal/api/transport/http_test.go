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
}

func (f *fakeAPI) SendPost(_ context.Context, id int64, channelID, message string) error {
	f.sent = append(f.sent, fmt.Sprintf("%d/%s/%s", id, channelID, message))
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
	assert.Equal(t, []string{"3/c1/hi"}, f.sent)

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
}
