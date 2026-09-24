package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

func setup(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	svc := api.NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	dist := fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}
	h, token := newBrowserHandler(svc, em, dist, fake, testAPI)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, token, fake
}

func TestIndexCarriesTokenAndIsNotCached(t *testing.T) {
	ts, token, _ := setup(t, false)
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `<meta name="spk-mattermost-api-token" content="`+token+`">`)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

func TestTestAPIAbsentByDefault(t *testing.T) {
	ts, token, _ := setup(t, false)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake-url", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestTestAPIFakeURLAndDeeplink(t *testing.T) {
	ts, token, fake := setup(t, true)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake-url", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var out map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, fake.URL(), out["url"])

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/_test/deeplink", strings.NewReader(`{"url":"mmauth://callback?MMAUTHTOKEN=x"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", ts.URL)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var e map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	assert.Equal(t, "no_pending_login", e["code"], "no login was started")
}
