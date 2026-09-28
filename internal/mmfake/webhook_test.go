package mmfake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestPostOverrideConfig(t *testing.T) {
	cfg := func(o Options) map[string]string {
		s := Start(o)
		defer s.Close()
		resp, err := http.Get(s.URL() + "/api/v4/config/client?format=old")
		require.NoError(t, err)
		defer resp.Body.Close()
		var m map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
		return m
	}
	on := cfg(Options{ImageProxy: true})
	assert.Equal(t, "true", on["EnablePostUsernameOverride"])
	assert.Equal(t, "true", on["EnablePostIconOverride"])
	assert.Equal(t, "true", on["HasImageProxy"])
	off := cfg(Options{DisablePostOverrides: true})
	assert.Equal(t, "false", off["EnablePostUsernameOverride"])
	assert.Equal(t, "false", off["EnablePostIconOverride"])
	assert.Equal(t, "false", off["HasImageProxy"])
}

func TestWebhookPostCarriesOverrides(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	p := s.WebhookPostAs("c-town", "bob", "pipeline passed", model.PostProps{OverrideUsername: "GitLab", OverrideIconURL: WebhookIconPath})
	a := loginAs(t, s, "alice")
	resp, body := a.raw("GET", "/api/v4/channels/c-town/posts?per_page=5", nil)
	require.Equal(t, 200, resp.StatusCode)
	var list model.PostList
	require.NoError(t, json.Unmarshal(body, &list))
	got := list.Posts[p.ID]
	assert.True(t, bool(got.Props.FromWebhook))
	assert.Equal(t, "GitLab", string(got.Props.OverrideUsername))
	assert.Equal(t, WebhookIconPath, got.Props.OverrideIconURL)
}

func TestStaticWebhookIconNeedsNoSession(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, err := http.Get(s.URL() + WebhookIconPath)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(b, []byte("\x89PNG")))
}

func TestImageProxy(t *testing.T) {
	ext := "https://gitlab.example/fox.png"
	q := "/api/v4/image?url=" + url.QueryEscape(ext)

	off := Start(Options{})
	defer off.Close()
	resp, _ := loginAs(t, off, "alice").raw("GET", q, nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "no proxy: the server refuses (MM-54477)")

	s := Start(Options{ImageProxy: true})
	defer s.Close()
	a := loginAs(t, s, "alice")
	resp, _ = a.raw("GET", q, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "an image the fake does not know")
	s.SetProxiedImage(ext, patternPNG(8, 8, palette[0]))
	resp, b := a.raw("GET", q, nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(b, []byte("\x89PNG")))
	assert.Equal(t, 2, s.Hits("GET", "/api/v4/image"))

	req, _ := http.NewRequest("GET", s.URL()+q, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the proxy needs a session")
}
