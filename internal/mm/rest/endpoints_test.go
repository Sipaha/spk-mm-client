package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientConfig(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/config/client", r.URL.Path)
		assert.Equal(t, "old", r.URL.Query().Get("format"))
		_, _ = w.Write([]byte(`{"SiteName":"MM","SiteURL":"https://mm.example.com","Version":"10.11.22","EnableSignUpWithGitLab":"true"}`))
	})
	cfg, err := c.ClientConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "MM", cfg.SiteName)
	assert.True(t, cfg.GitLabEnabled())
}

func TestPingRejectsNonOKStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"FAIL"}`)) })
	assert.Error(t, c.Ping(context.Background()))
}

func TestLoginReturnsTokenFromHeader(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/users/login", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"), "login must not send a stale token")
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]string{"login_id": "alice", "password": "pw"}, body)
		w.Header().Set("Token", "new-token")
		_, _ = w.Write([]byte(`{"id":"u1","username":"alice"}`))
	})
	tok, u, err := c.Login(context.Background(), "alice", "pw")
	require.NoError(t, err)
	assert.Equal(t, "new-token", tok)
	assert.Equal(t, "alice", u.Username)
}

func TestLoginWithoutTokenHeaderFails(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"id":"u1"}`)) })
	_, _, err := c.Login(context.Background(), "alice", "pw")
	assert.Error(t, err)
}

func TestLogoutTreats401AsSuccess(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	assert.NoError(t, c.Logout(context.Background()))
}

func TestMe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/me", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"u1","username":"alice","first_name":"Alice"}`))
	})
	u, err := c.Me(context.Background())
	require.NoError(t, err)
	assert.Equal(t, User{ID: "u1", Username: "alice", FirstName: "Alice"}, u)
}

func TestClientConfigCustomEmojiFlag(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"SiteName":"MM","EnableCustomEmoji":"true"}`))
	})
	cfg, err := c.ClientConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true", cfg.EnableCustomEmoji)
}
