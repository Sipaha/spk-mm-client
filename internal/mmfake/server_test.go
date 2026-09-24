package mmfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestPasswordLoginMeLogout(t *testing.T) {
	s := Start(Options{})
	defer s.Close()

	resp, err := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"secret"}`))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	tok := resp.Header.Get("Token")
	require.NotEmpty(t, tok)
	assert.Equal(t, 1, s.ActiveSessions())

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/api/v4/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var me map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	assert.Equal(t, "alice", me["username"])

	req, _ = http.NewRequest(http.MethodPost, s.URL()+"/api/v4/users/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	_, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 0, s.ActiveSessions())
}

func TestWrongPasswordIs401(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, err := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"nope"}`))
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestGitLabMobileFlowEndsWithMMAuthLink(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	c := noRedirect()

	resp, err := c.Get(s.URL() + "/oauth/gitlab/mobile_login?redirect_to=" + url.QueryEscape("mmauth://callback"))
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	authorize := resp.Header.Get("Location")
	require.Contains(t, authorize, "/mmfake/gitlab/authorize?state=")

	resp, err = c.Get(s.URL() + authorize)
	require.NoError(t, err)
	page, _ := io.ReadAll(resp.Body)
	m := regexp.MustCompile(`id="authorize" href="([^"]+)"`).FindSubmatch(page)
	require.NotNil(t, m)

	resp, err = c.Get(s.URL() + string(m[1]))
	require.NoError(t, err)
	page, _ = io.ReadAll(resp.Body)
	link := regexp.MustCompile(`id="mmauth-link" href="([^"]+)"`).FindSubmatch(page)
	require.NotNil(t, link)
	u, err := url.Parse(strings.ReplaceAll(string(link[1]), "&amp;", "&"))
	require.NoError(t, err)
	assert.Equal(t, "mmauth", u.Scheme)
	assert.NotEmpty(t, u.Query().Get("MMAUTHTOKEN"))
	assert.Equal(t, s.URL(), u.Query().Get("srv"))
	assert.Equal(t, 1, s.ActiveSessions())
}

func TestMobileLoginRejectsNonMMAuthRedirect(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, err := noRedirect().Get(s.URL() + "/oauth/gitlab/mobile_login?redirect_to=" + url.QueryEscape("http://127.0.0.1:1/cb"))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "Invalid custom url scheme")
}

func TestRevokeAllKillsSessions(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, _ := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"secret"}`))
	tok := resp.Header.Get("Token")
	s.RevokeAll()
	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/api/v4/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}
