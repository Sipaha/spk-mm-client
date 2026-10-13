package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spk/spk-mm-client/internal/media"
	"github.com/stretchr/testify/require"
)

func TestMediaAccountIsolation(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" }, "never live")
	cache, err := media.New(media.Options{Dir: t.TempDir(), Origin: f.svc})
	require.NoError(t, err)
	ts := httptest.NewServer(cache)
	defer ts.Close()
	alice := f.server(id).MediaScope
	require.NotEmpty(t, alice)
	get := func(scope string) int {
		t.Helper()
		r, err := http.Get(fmt.Sprintf("%s/media/%d/thumb/f-build?account=%s", ts.URL, id, scope))
		require.NoError(t, err)
		defer r.Body.Close()
		_, err = io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		return r.StatusCode
	}
	require.Equal(t, 404, get(""), "unscoped URLs cannot authorize a read")
	require.Equal(t, 200, get(alice))
	f.offline(fake, id)
	require.Equal(t, 200, get(alice), "own disk cache remains readable offline")
	fake.SetDown(false)
	require.NoError(t, f.svc.Logout(context.Background(), id))
	require.Equal(t, 404, get(alice), "logout denies even a positive cache hit")
	_, err = f.svc.LoginWithPassword(context.Background(), id, "bob", "secret")
	require.NoError(t, err)
	f.eventually(func() bool { return f.server(id).State == "live" }, "bob never live")
	bob := f.server(id).MediaScope
	require.NotEqual(t, alice, bob)
	require.Equal(t, 404, get(alice), "old URL cannot read as another account")
	// A controlled forbidden upstream proves the cache cannot bypass the
	// current account's authorization; no claim about real seed permissions.
	f.svc.transfer = &http.Client{Transport: accountDenyTransport{}}
	require.Equal(t, 403, get(bob), "bob must fetch instead of receiving alice's bytes")
	require.NoError(t, f.svc.Logout(context.Background(), id))
	_, err = f.svc.LoginWithPassword(context.Background(), id, "alice", "secret")
	require.NoError(t, err)
	require.Equal(t, alice, f.server(id).MediaScope)
	require.Equal(t, 200, get(alice), "same account reuses its cache without upstream")
}

type accountDenyTransport struct{}

func (accountDenyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 403, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
}
