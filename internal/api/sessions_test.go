package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/auth"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/store"
)

// Hold exactly one network operation. A recovery must cancel the old
// request rather than wait for its answer; later requests pass normally.
func TestSessionChangeCancelsPendingAuthentication(t *testing.T) {
	for _, method := range []string{"sso", "password"} {
		for _, stage := range []string{"authenticate", "revoke"} {
			for _, action := range []string{"logout", "remove", "new-login", "close", "caller-cancel"} {
				t.Run(method+"/"+stage+"/"+action, func(t *testing.T) {
					var armed atomic.Bool
					held, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
					var releaseOnce sync.Once
					unblock := func() { releaseOnce.Do(func() { close(release) }) }
					defer unblock()
					path := "/api/v4/users/me"
					if method == "password" {
						path = "/api/v4/users/login"
					}
					if stage == "revoke" {
						path = "/api/v4/users/logout"
					}
					fake := mmfake.Start(mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
						if r.URL.Path != path || !armed.CompareAndSwap(true, false) {
							return false
						}
						// Let net/http observe connection cancellation after a POST's
						// body, preserving it if the test releases the handler normally.
						if r.Method == http.MethodPost {
							body, err := io.ReadAll(r.Body)
							if err != nil {
								return true
							}
							r.Body = io.NopCloser(bytes.NewReader(body))
						}
						close(held)
						select {
						case <-r.Context().Done():
							close(cancelled)
							return true
						case <-release:
							return false
						}
					}})
					t.Cleanup(fake.Close)
					f := newChatFixture(t)
					ctx := context.Background()
					id := f.signIn(fake, "alice")
					f.eventually(func() bool { return f.server(id).State == "live" && f.loaded(id, "c-town") }, "initial session")
					callback := ""
					if method == "sso" {
						require.NoError(t, f.svc.StartGitLabLogin(ctx, id))
						callback = completeGitLab(t, auth.GitLabLoginURL(fake.URL()))
					}
					callCtx, cancelCall := context.WithCancel(ctx)
					defer cancelCall()
					armed.Store(true)
					done := make(chan error, 1)
					go func() {
						if method == "sso" {
							done <- f.svc.HandleDeepLink(callCtx, callback)
						} else {
							_, err := f.svc.LoginWithPassword(callCtx, id, "alice", "secret")
							done <- err
						}
					}()
					select {
					case <-held:
					case <-time.After(3 * time.Second):
						t.Fatal("login never reached held stage")
					}
					switch action {
					case "logout":
						require.NoError(t, f.svc.Logout(ctx, id))
					case "remove":
						require.NoError(t, f.svc.RemoveServer(ctx, id))
					case "new-login":
						_, err := f.svc.LoginWithPassword(ctx, id, "bob", "secret")
						require.NoError(t, err)
					case "close":
						f.svc.Close()
					case "caller-cancel":
						cancelCall()
					}
					select {
					case <-cancelled:
					case <-time.After(time.Second):
						t.Fatal("old network request was not cancelled")
					}
					unblock() // even a late server response cannot restore the old login
					select {
					case err := <-done:
						if method == "sso" {
							require.NoError(t, err, "obsolete callback is silent")
						} else {
							assert.Equal(t, CodeCancelled, codeOf(err))
						}
					case <-time.After(3 * time.Second):
						t.Fatal("cancelled authentication did not finish")
					}
					srv, err := f.st.GetServer(ctx, id)
					if action == "remove" {
						assert.ErrorIs(t, err, store.ErrNotFound)
						assert.Nil(t, f.svc.manager().Worker(id))
						return
					}
					require.NoError(t, err)
					switch action {
					case "logout":
						assert.False(t, srv.SignedIn())
						assert.Nil(t, f.svc.manager().Worker(id))
					case "new-login":
						assert.Equal(t, "bob", srv.Username)
						f.eventually(func() bool { return f.server(id).State == "live" }, "latest login is live")
					case "close":
						assert.Equal(t, "alice", srv.Username)
						assert.Nil(t, f.svc.manager().Worker(id))
					case "caller-cancel":
						assert.Equal(t, "alice", srv.Username)
						require.NotNil(t, f.svc.manager().Worker(id), "restore old worker after a cancelled replace")
					}
				})
			}
		}
	}
}

func TestClosedServiceRejectsNewAuthentication(t *testing.T) {
	f := newFixture(t)
	srv, err := f.svc.AddServer(context.Background(), f.fake.URL())
	require.NoError(t, err)
	f.svc.Close()
	_, err = f.svc.LoginWithPassword(context.Background(), srv.ID, "alice", "secret")
	assert.Equal(t, CodeCancelled, codeOf(err))
	assert.Equal(t, CodeCancelled, codeOf(f.svc.StartGitLabLogin(context.Background(), srv.ID)))
	assert.Equal(t, CodeCancelled, codeOf(f.svc.Start(context.Background())))
	assert.Zero(t, f.fake.Hits("POST", "/api/v4/users/login"))
}
