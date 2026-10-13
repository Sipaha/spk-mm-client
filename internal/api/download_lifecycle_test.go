package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/stretchr/testify/require"
)

func TestDownloadOwnedByServiceAndSession(t *testing.T) {
	for _, action := range []string{"close", "logout", "switch", "waiter"} {
		t.Run(action, func(t *testing.T) {
			var armed atomic.Bool
			held, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var data []byte
			fake := mmfake.Start(mmfake.Options{RequestHook: func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v4/files/f-build" || !armed.CompareAndSwap(true, false) {
					return false
				}
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				close(held)
				select {
				case <-release:
					_, _ = w.Write(data)
				case <-r.Context().Done():
					close(canceled)
				}
				return true
			}})
			t.Cleanup(fake.Close)
			f := newChatFixture(t)
			id := f.signIn(fake, "alice")
			f.eventually(func() bool { return f.server(id).State == "live" }, "never live")
			dir := t.TempDir()
			f.svc.getenv = downloadsIn(dir)
			resp, err := f.svc.Get(context.Background(), id, "/api/v4/files/f-build", nil)
			require.NoError(t, err)
			data, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			_ = resp.Body.Close()
			armed.Store(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := f.svc.DownloadFile(ctx, id, "f-build"); done <- err }()
			select {
			case <-held:
			case <-time.After(3 * time.Second):
				t.Fatal("download not held")
			}
			switch action {
			case "close":
				f.svc.Close()
			case "logout":
				require.NoError(t, f.svc.Logout(context.Background(), id))
			case "switch":
				_, err = f.svc.LoginWithPassword(context.Background(), id, "bob", "secret")
				require.NoError(t, err)
			case "waiter":
				second := make(chan error, 1)
				go func() { _, err := f.svc.DownloadFile(context.Background(), id, "f-build"); second <- err }()
				cancel()
				require.Error(t, <-done)
				select {
				case <-canceled:
					t.Fatal("waiter canceled shared owner")
				case <-time.After(50 * time.Millisecond):
				}
				close(release)
				require.NoError(t, <-second)
				require.FileExists(t, filepath.Join(dir, "build.png"))
				assertNoPart(t, dir)
				return
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not cancel upstream")
			}
			require.Error(t, <-done)
			assertNoPart(t, dir)
			files, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, files)
			f.svc.filesMu.Lock()
			require.Empty(t, f.svc.saving)
			require.Empty(t, f.svc.progress)
			f.svc.filesMu.Unlock()
			if action == "close" {
				_, err = f.svc.OpenFile(context.Background(), id, "f-build")
				require.Error(t, err)
			}
		})
	}
}
