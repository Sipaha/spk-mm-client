package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
)

type fakeAPI struct{ added string }

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
