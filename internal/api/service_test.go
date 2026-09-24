package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/auth"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

type fixture struct {
	svc    *Service
	fake   *mmfake.Server
	opened []string
	evs    <-chan events.Event
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	em := events.NewEmitter()
	ch, unsub := em.Subscribe()
	t.Cleanup(unsub)
	f := &fixture{fake: fake, evs: ch}
	f.svc = NewService(st, em, func(u string) error { f.opened = append(f.opened, u); return nil }, &http.Client{Timeout: 5 * time.Second})
	return f
}

func codeOf(err error) string {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func (f *fixture) nextEvent(t *testing.T, typ string) events.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-f.evs:
			if ev.Type == typ {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", typ)
		}
	}
}

// completeGitLab walks the fake GitLab consent flow like the user's browser
// would and returns the mmauth:// callback URL.
func completeGitLab(t *testing.T, loginURL string) string {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(loginURL)
	require.NoError(t, err)
	u, _ := url.Parse(loginURL)
	base := u.Scheme + "://" + u.Host
	pu, _ := url.Parse(resp.Header.Get("Location"))
	resp, err = c.Get(base + "/mmfake/gitlab/complete?state=" + pu.Query().Get("state"))
	require.NoError(t, err)
	page, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	m := regexp.MustCompile(`id="mmauth-link" href="([^"]+)"`).FindSubmatch(page)
	require.NotNil(t, m, "complete page has no mmauth link")
	return strings.ReplaceAll(string(m[1]), "&amp;", "&")
}

func TestAddServerUsesSiteNameAndGitLabFlag(t *testing.T) {
	f := newFixture(t)
	dto, err := f.svc.AddServer(context.Background(), f.fake.URL()+"/")
	require.NoError(t, err)
	assert.Equal(t, "Fake MM", dto.Name)
	assert.Equal(t, f.fake.URL(), dto.URL)
	assert.True(t, dto.GitLab)
	assert.False(t, dto.SignedIn)
	f.nextEvent(t, EventServersChanged)

	_, err = f.svc.AddServer(context.Background(), f.fake.URL())
	assert.Equal(t, CodeServerExists, codeOf(err))
}

func TestAddServerInvalidURL(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.AddServer(context.Background(), "ftp://x")
	assert.Equal(t, CodeInvalidURL, codeOf(err))
}

func TestAddServerNotMattermost(t *testing.T) {
	f := newFixture(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	_, err := f.svc.AddServer(context.Background(), other.URL)
	assert.Equal(t, CodeNotMattermost, codeOf(err))
}

func TestAddServerUnreachable(t *testing.T) {
	f := newFixture(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	u := dead.URL
	dead.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.svc.AddServer(ctx, u)
	assert.Equal(t, CodeUnreachable, codeOf(err))
}

func TestPasswordLoginAndLogout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, err := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, err)

	_, err = f.svc.LoginWithPassword(ctx, dto.ID, "alice", "wrong")
	assert.Equal(t, CodeBadCredentials, codeOf(err))

	dto, err = f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	assert.True(t, dto.SignedIn)
	assert.Equal(t, "alice", dto.Username)
	assert.Equal(t, 1, f.fake.ActiveSessions())

	require.NoError(t, f.svc.Logout(ctx, dto.ID))
	assert.Equal(t, 0, f.fake.ActiveSessions(), "logout revokes server-side")
	list, _ := f.svc.ListServers(ctx)
	assert.False(t, list[0].SignedIn)
}

func TestLogoutClearsEvenIfServerRejects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	_, err := f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	f.fake.Close() // server gone entirely
	require.NoError(t, f.svc.Logout(ctx, dto.ID))
	list, _ := f.svc.ListServers(ctx)
	assert.False(t, list[0].SignedIn)
}

func TestGitLabSSOEndToEnd(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, f.svc.StartGitLabLogin(ctx, dto.ID))
	require.Len(t, f.opened, 1)
	assert.Equal(t, auth.GitLabLoginURL(f.fake.URL()), f.opened[0])

	cb := completeGitLab(t, f.opened[0])
	require.NoError(t, f.svc.HandleDeepLink(ctx, cb))
	list, _ := f.svc.ListServers(ctx)
	assert.True(t, list[0].SignedIn)
	assert.Equal(t, "alice", list[0].Username)
}

func TestHandleDeepLinkDuplicateIsSilent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, f.svc.StartGitLabLogin(ctx, dto.ID))
	cb := completeGitLab(t, f.opened[0])
	require.NoError(t, f.svc.HandleDeepLink(ctx, cb))
	f.nextEvent(t, EventServersChanged)
	assert.NoError(t, f.svc.HandleDeepLink(ctx, cb))
	select {
	case ev := <-f.evs:
		assert.NotEqual(t, EventLoginFailed, ev.Type)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleDeepLinkWithRevokedTokenFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, f.svc.StartGitLabLogin(ctx, dto.ID))
	cb := completeGitLab(t, f.opened[0])
	f.fake.RevokeAll()
	err := f.svc.HandleDeepLink(ctx, cb)
	assert.Equal(t, CodeAuthFailed, codeOf(err))
	ev := f.nextEvent(t, EventLoginFailed)
	assert.Equal(t, CodeAuthFailed, ev.Payload["code"])
	list, _ := f.svc.ListServers(ctx)
	assert.False(t, list[0].SignedIn)
}

func TestStartGitLabLoginDisabled(t *testing.T) {
	f := newFixture(t)
	noGitLab := mmfake.Start(mmfake.Options{SiteName: "NoGL", DisableGitLab: true})
	defer noGitLab.Close()
	dto, err := f.svc.AddServer(context.Background(), noGitLab.URL())
	require.NoError(t, err)
	assert.Equal(t, CodeGitLabDisabled, codeOf(f.svc.StartGitLabLogin(context.Background(), dto.ID)))
}

func TestRemoveServerRevokesSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	_, err := f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	require.NoError(t, f.svc.RemoveServer(ctx, dto.ID))
	assert.Equal(t, 0, f.fake.ActiveSessions())
	list, _ := f.svc.ListServers(ctx)
	assert.Empty(t, list)
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RemoveServer(ctx, dto.ID)))
}

func TestServerDTOHasNoToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	_, err := f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	list, _ := f.svc.ListServers(ctx)
	raw, _ := json.Marshal(list)
	var generic []map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	assert.ElementsMatch(t, []string{"id", "name", "url", "signed_in", "username", "gitlab"}, keys(generic[0]))
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
