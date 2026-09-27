package attach

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

var errNotSignedIn = errors.New("not signed in")

// fakeUploader uploads by reading the body in small chunks (reporting
// progress after each); send, when set, replaces that.
type fakeUploader struct {
	mu      sync.Mutex
	calls   []string // client ids, in order
	bodies  map[string][]byte
	auth    []error
	running atomic.Int32
	peak    atomic.Int32
	send    func(ctx context.Context, body io.Reader, size int64, progress func(int64)) error
}

func (u *fakeUploader) UploadFile(ctx context.Context, _, filename, clientID string, body io.Reader, size int64, progress func(sent int64)) (model.FileInfo, error) {
	n := u.running.Add(1)
	defer u.running.Add(-1)
	for {
		p := u.peak.Load()
		if n <= p || u.peak.CompareAndSwap(p, n) {
			break
		}
	}
	u.mu.Lock()
	u.calls = append(u.calls, clientID)
	send := u.send
	u.mu.Unlock()
	if send != nil {
		if err := send(ctx, body, size, progress); err != nil {
			return model.FileInfo{}, err
		}
		return model.FileInfo{ID: "f-" + clientID, Name: filename, Size: size}, nil
	}
	var got bytes.Buffer
	buf := make([]byte, 4)
	for {
		n, err := body.Read(buf)
		got.Write(buf[:n])
		if n > 0 && progress != nil {
			progress(int64(got.Len()))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return model.FileInfo{}, err
		}
	}
	u.mu.Lock()
	if u.bodies == nil {
		u.bodies = map[string][]byte{}
	}
	u.bodies[clientID] = got.Bytes()
	u.mu.Unlock()
	return model.FileInfo{ID: "f-" + clientID, Name: filename, Size: int64(got.Len())}, nil
}

func (u *fakeUploader) CheckAuth(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.auth = append(u.auth, err)
}

func (u *fakeUploader) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

func (u *fakeUploader) body(id string) []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.bodies[id]
}

func (u *fakeUploader) setSend(fn func(ctx context.Context, body io.Reader, size int64, progress func(int64)) error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.send = fn
}

type fakeBackend struct {
	mu     sync.Mutex
	limits map[int64]Limits
	live   map[int64]bool
	up     *fakeUploader
}

func (b *fakeBackend) Limits(srv int64) (Limits, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, ok := b.limits[srv]
	if !ok {
		return Limits{}, errNotSignedIn
	}
	return l, nil
}

func (b *fakeBackend) Uploader(srv int64) Uploader {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.live[srv] {
		return nil
	}
	return b.up
}

func (b *fakeBackend) setLive(srv int64, live bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.live[srv] = live
}

type changes struct {
	mu   sync.Mutex
	list []string
}

func (c *changes) add(_ int64, ch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, ch)
}

func (c *changes) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.list)
}

type env struct {
	t   *testing.T
	s   *Store
	b   *fakeBackend
	up  *fakeUploader
	dir string
	chg *changes
}

// newEnv: server 1 is signed in and live with a 1 MiB limit; server 2 is
// signed in, offline; others are not signed in.
func newEnv(t *testing.T, tune ...func(*Options)) *env {
	t.Helper()
	up := &fakeUploader{}
	b := &fakeBackend{
		limits: map[int64]Limits{1: {Enabled: true, MaxFileSize: 1 << 20}, 2: {Enabled: true, MaxFileSize: 1 << 20}},
		live:   map[int64]bool{1: true},
		up:     up,
	}
	e := &env{t: t, b: b, up: up, dir: filepath.Join(t.TempDir(), "tmp"), chg: &changes{}}
	o := Options{Dir: e.dir, Backend: b, OnChange: e.chg.add, ProgressEvery: time.Hour, Stall: time.Minute}
	for _, fn := range tune {
		fn(&o)
	}
	e.s = New(o)
	t.Cleanup(e.s.Close)
	return e
}

func (e *env) file(name, content string) string {
	e.t.Helper()
	p := filepath.Join(e.t.TempDir(), name)
	require.NoError(e.t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func (e *env) state(id string) (Attachment, bool) {
	return e.s.Get(id)
}

func (e *env) waitState(id string, want State) Attachment {
	e.t.Helper()
	var a Attachment
	require.Eventually(e.t, func() bool {
		var ok bool
		a, ok = e.state(id)
		return ok && a.State == want
	}, 5*time.Second, 5*time.Millisecond, "attachment never became %s (now %+v)", want, a)
	return a
}

func codeOf(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func spools(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	return out
}

func TestAddPathChecksTheFileAndTheServerLimits(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false) // keep everything staged
	ok := e.file("notes.txt", "hello")
	a, err := e.s.AddPath(1, "c1", ok)
	require.NoError(t, err)
	assert.Equal(t, "notes.txt", a.Name)
	assert.Equal(t, int64(5), a.Size)
	assert.Equal(t, "text/plain", a.Mime)
	assert.Equal(t, StateStaged, a.State)
	assert.Regexp(t, `^[a-z0-9]{26}$`, a.ID, "an id the server accepts as client_id and the media route as a key")

	_, err = e.s.AddPath(1, "c1", filepath.Dir(ok))
	assert.Equal(t, CodeNotAFile, codeOf(err), "a directory")
	_, err = e.s.AddPath(1, "c1", filepath.Join(filepath.Dir(ok), "missing"))
	assert.Equal(t, CodeNotAFile, codeOf(err), "a missing file")
	_, err = e.s.AddPath(1, "c1", "notes.txt")
	assert.Equal(t, CodeNotAFile, codeOf(err), "a relative path")
	_, err = e.s.AddPath(1, "c1", e.file("big.bin", strings.Repeat("x", 1<<20+1)))
	assert.Equal(t, CodeTooLarge, codeOf(err))
	_, err = e.s.AddPath(9, "c1", ok)
	assert.ErrorIs(t, err, errNotSignedIn, "the backend's error passes through")

	e.b.mu.Lock()
	e.b.limits[2] = Limits{Enabled: false, MaxFileSize: 1 << 20}
	e.b.mu.Unlock()
	_, err = e.s.AddPath(2, "c1", ok)
	assert.Equal(t, CodeDisabled, codeOf(err))

	e.b.mu.Lock()
	e.b.limits[2] = Limits{} // config not read yet: the server decides
	e.b.mu.Unlock()
	_, err = e.s.AddPath(2, "c1", e.file("big2.bin", strings.Repeat("x", 1<<20+1)))
	assert.NoError(t, err, "an unknown config refuses nothing")

	assert.Len(t, e.s.List(1, "c1"), 1)
	assert.Empty(t, e.s.List(1, "c2"))
	assert.NotNil(t, e.s.List(1, "c2"), "an empty list, not null, in JSON")
}

func TestAtMostTenPerChannel(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	p := e.file("a.txt", "a")
	for i := 0; i < MaxPerChannel; i++ {
		_, err := e.s.AddPath(1, "c1", p)
		require.NoError(t, err)
	}
	_, err := e.s.AddPath(1, "c1", p)
	assert.Equal(t, CodeTooMany, codeOf(err))
	_, err = e.s.AddBytes(1, "c1", "x.png", "image/png", strings.NewReader("x"), 0)
	assert.Equal(t, CodeTooMany, codeOf(err))
	assert.Empty(t, spools(t, e.dir), "a refused byte upload leaves no spool")
	_, err = e.s.AddPath(1, "c2", p)
	assert.NoError(t, err, "another channel has its own ten")
}

func TestAddBytesSpoolsUnderTheLimit(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddBytes(1, "c1", "../../Screenshot.png", "", bytes.NewReader(pngHeader()), 0)
	require.NoError(t, err)
	assert.Equal(t, "Screenshot.png", a.Name, "no directories in the name")
	assert.Equal(t, "image/png", a.Mime, "sniffed when not given")
	assert.Equal(t, []string{"attach-" + a.ID}, spools(t, e.dir))
	data, err := os.ReadFile(filepath.Join(e.dir, "attach-"+a.ID))
	require.NoError(t, err)
	assert.Equal(t, pngHeader(), data)
	fi, err := os.Stat(filepath.Join(e.dir, "attach-"+a.ID))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	_, err = e.s.AddBytes(1, "c1", "big.bin", "application/octet-stream", strings.NewReader(strings.Repeat("x", 11)), 10)
	assert.Equal(t, CodeTooLarge, codeOf(err), "over the caller's limit")
	_, err = e.s.AddBytes(1, "c1", "big.bin", "application/octet-stream", strings.NewReader(strings.Repeat("x", 1<<20+1)), 0)
	assert.Equal(t, CodeTooLarge, codeOf(err), "over the server's limit")
	assert.Len(t, spools(t, e.dir), 1, "refused bodies are not left on disk")

	a2, err := e.s.AddBytes(1, "c1", "", "text/plain; charset=utf-8", strings.NewReader("hi"), 0)
	require.NoError(t, err)
	assert.Equal(t, "attachment", a2.Name)
	assert.Equal(t, "text/plain", a2.Mime)
}

func pngHeader() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
}

func TestUploadReportsThrottledProgress(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.ProgressEvery = 40 * time.Millisecond })
	e.up.setSend(func(_ context.Context, body io.Reader, _ int64, progress func(int64)) error {
		var n int64
		buf := make([]byte, 10)
		for {
			k, err := body.Read(buf)
			n += int64(k)
			if k > 0 {
				progress(n)
				time.Sleep(2 * time.Millisecond)
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	})
	a, err := e.s.AddPath(1, "c1", e.file("data.bin", strings.Repeat("y", 1000))) // 100 chunks, ~200+ ms
	require.NoError(t, err)
	done := e.waitState(a.ID, StateUploaded)
	assert.Equal(t, "f-"+a.ID, done.FileID, "the id doubles as client_id")
	assert.Equal(t, int64(1000), done.Sent)
	assert.Empty(t, done.Error)
	n := e.chg.count()
	assert.Greater(t, n, 3, "progress was reported")
	assert.Less(t, n, 30, "but not per chunk")
}

func TestUploadStreamsTheFileAsIs(t *testing.T) {
	e := newEnv(t)
	a, err := e.s.AddPath(1, "c1", e.file("x.txt", "the content"))
	require.NoError(t, err)
	e.waitState(a.ID, StateUploaded)
	assert.Equal(t, "the content", string(e.up.body(a.ID)))
	b, err := e.s.AddBytes(1, "c1", "y.txt", "text/plain", strings.NewReader("spooled"), 0)
	require.NoError(t, err)
	e.waitState(b.ID, StateUploaded)
	assert.Equal(t, "spooled", string(e.up.body(b.ID)))
}

func TestChangedOrMissingFileFailsItsUpload(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	p := e.file("x.txt", "12345")
	a, err := e.s.AddPath(1, "c1", p)
	require.NoError(t, err)
	gone := e.file("y.txt", "abc")
	b, err := e.s.AddPath(1, "c1", gone)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, []byte("123456"), 0o600))
	require.NoError(t, os.Remove(gone))
	e.b.setLive(1, true)
	e.s.Wake(1)
	assert.Equal(t, CodeChanged, e.waitState(a.ID, StateFailed).Error)
	assert.Equal(t, CodeChanged, e.waitState(b.ID, StateFailed).Error)
	assert.Zero(t, e.up.callCount(), "nothing was sent")

	// Same size, new mtime: changed too.
	p2 := e.file("z.txt", "aaaa")
	e.b.setLive(1, false)
	c, err := e.s.AddPath(1, "c1", p2)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(p2, time.Now(), time.Now().Add(time.Hour)))
	e.b.setLive(1, true)
	e.s.Wake(1)
	assert.Equal(t, CodeChanged, e.waitState(c.ID, StateFailed).Error)
}

func TestOfflineAttachmentsWaitForLive(t *testing.T) {
	e := newEnv(t)
	a, err := e.s.AddPath(2, "c1", e.file("x.txt", "abc"))
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	got, _ := e.state(a.ID)
	assert.Equal(t, StateStaged, got.State)
	assert.Zero(t, e.up.callCount())
	e.b.setLive(2, true)
	e.s.Wake(2)
	e.waitState(a.ID, StateUploaded)
}

func blockUntilCancelled(started chan<- struct{}) func(ctx context.Context, _ io.Reader, _ int64, _ func(int64)) error {
	return func(ctx context.Context, _ io.Reader, _ int64, _ func(int64)) error {
		started <- struct{}{}
		<-ctx.Done()
		return &rest.Error{Kind: rest.KindNetwork, Err: ctx.Err()}
	}
}

func TestRemoveCancelsTheUploadAndDeletesTheSpool(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	a, err := e.s.AddBytes(1, "c1", "x.bin", "application/octet-stream", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	<-started
	assert.Equal(t, StateUploading, e.waitState(a.ID, StateUploading).State)
	require.NoError(t, e.s.Remove(a.ID))
	_, ok := e.state(a.ID)
	assert.False(t, ok)
	assert.Empty(t, e.s.List(1, "c1"))
	assert.Empty(t, spools(t, e.dir))
	require.Eventually(t, func() bool { return e.up.running.Load() == 0 }, 5*time.Second, 5*time.Millisecond, "the request was not cancelled")
	assert.Equal(t, CodeNotFound, codeOf(e.s.Remove(a.ID)))
}

func TestUnauthorizedFailsAndAsksForSignInWithoutRetrying(t *testing.T) {
	e := newEnv(t)
	e.up.setSend(func(context.Context, io.Reader, int64, func(int64)) error {
		return &rest.Error{Kind: rest.KindAuth, Status: http.StatusUnauthorized, Err: errors.New("401")}
	})
	a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abc"))
	require.NoError(t, err)
	assert.Equal(t, CodeSessionExpired, e.waitState(a.ID, StateFailed).Error)
	e.up.mu.Lock()
	assert.Len(t, e.up.auth, 1, "the 401 went to the worker's re-auth path")
	e.up.mu.Unlock()
	e.s.Wake(1)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, e.up.callCount(), "never retried by itself")

	e.up.setSend(nil)
	require.NoError(t, e.s.Retry(a.ID))
	e.waitState(a.ID, StateUploaded)
	assert.Equal(t, 2, e.up.callCount())
	assert.Equal(t, CodeNotFound, codeOf(e.s.Retry("nope")))
}

func TestUploadErrorsAreClassified(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&rest.Error{Kind: rest.KindTooLarge, Status: 413}, CodeTooLarge},
		{&rest.Error{Kind: rest.KindAuth, Status: 403, ID: "api.file.attachments.disabled.app_error"}, CodeDisabled},
		{&rest.Error{Kind: rest.KindAuth, Status: 403}, CodeForbidden},
		{&rest.Error{Kind: rest.KindNetwork, Status: 502}, CodeUnreachable},
		{&rest.Error{Kind: rest.KindAPI, Status: 400}, CodeInternal},
	} {
		e := newEnv(t)
		e.up.setSend(func(context.Context, io.Reader, int64, func(int64)) error { return tc.err })
		a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abc"))
		require.NoError(t, err)
		assert.Equal(t, tc.code, e.waitState(a.ID, StateFailed).Error, "%v", tc.err)
	}
}

func TestStalledUploadFails(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Stall = 80 * time.Millisecond })
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abc"))
	require.NoError(t, err)
	assert.Equal(t, CodeUnreachable, e.waitState(a.ID, StateFailed).Error)
}

func TestProgressKeepsAStallTimerAway(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Stall = 60 * time.Millisecond })
	e.up.setSend(func(ctx context.Context, _ io.Reader, _ int64, progress func(int64)) error {
		for i := 1; i <= 10; i++ { // 200 ms in all, never 60 ms without a byte
			select {
			case <-ctx.Done():
				return &rest.Error{Kind: rest.KindNetwork, Err: ctx.Err()}
			case <-time.After(20 * time.Millisecond):
			}
			progress(int64(i))
		}
		return nil
	})
	a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abcdefghij"))
	require.NoError(t, err)
	e.waitState(a.ID, StateUploaded)
}

func TestTwoUploadsAtATimePerServer(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.up.setSend(func(ctx context.Context, _ io.Reader, _ int64, _ func(int64)) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	var ids []string
	for i := 0; i < 5; i++ {
		a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abc"))
		require.NoError(t, err)
		ids = append(ids, a.ID)
	}
	require.Eventually(t, func() bool { return e.up.running.Load() == 2 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, int32(2), e.up.peak.Load())
	close(release)
	for _, id := range ids {
		e.waitState(id, StateUploaded)
	}
	assert.Equal(t, int32(2), e.up.peak.Load())
}

func TestWaitReportsTheOutcome(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abc"))
	require.NoError(t, err)
	b, err := e.s.AddPath(1, "c1", e.file("y.txt", "abc"))
	require.NoError(t, err)
	res := make(chan error, 1)
	go func() { res <- e.s.Wait(context.Background(), []string{a.ID, b.ID}) }()
	select {
	case err := <-res:
		t.Fatalf("returned while staged: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	e.b.setLive(1, true)
	e.s.Wake(1)
	require.NoError(t, <-res)

	e.up.setSend(func(context.Context, io.Reader, int64, func(int64)) error {
		return &rest.Error{Kind: rest.KindNetwork, Err: errors.New("down")}
	})
	c, err := e.s.AddPath(1, "c1", e.file("z.txt", "abc"))
	require.NoError(t, err)
	assert.Equal(t, CodeUnreachable, codeOf(e.s.Wait(context.Background(), []string{a.ID, c.ID})))
	require.NoError(t, e.s.Remove(c.ID))
	assert.Equal(t, CodeNotFound, codeOf(e.s.Wait(context.Background(), []string{c.ID})))

	e.b.setLive(1, false)
	d, err := e.s.AddPath(1, "c1", e.file("w.txt", "abc"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, e.s.Wait(ctx, []string{d.ID}), context.DeadlineExceeded)
}

func TestPauseReturnsRunningUploadsToTheQueue(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	a, err := e.s.AddPath(1, "c1", e.file("x.txt", "abc"))
	require.NoError(t, err)
	<-started
	e.s.Pause(1) // the worker stops: its token may be revoked next
	e.waitState(a.ID, StateStaged)
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 1, e.up.callCount(), "paused: not started again while the old worker is still around")
	e.up.setSend(nil)
	e.s.Wake(1) // the new worker is live
	e.waitState(a.ID, StateUploaded)
}

func TestDropServerForgetsItsAttachments(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddBytes(1, "c1", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	b, err := e.s.AddBytes(2, "c1", "y.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	e.s.DropServer(1)
	_, ok := e.state(a.ID)
	assert.False(t, ok)
	_, ok = e.state(b.ID)
	assert.True(t, ok)
	assert.Equal(t, []string{"attach-" + b.ID}, spools(t, e.dir))
}

func TestCloseCancelsUploadsAndDeletesSpools(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	_, err := e.s.AddBytes(1, "c1", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	<-started
	e.s.Close()
	assert.Zero(t, e.up.running.Load(), "Close waits for cancelled uploads")
	assert.Empty(t, spools(t, e.dir))
	_, err = e.s.AddBytes(1, "c1", "x.bin", "", strings.NewReader("abc"), 0)
	assert.Error(t, err)
	assert.Empty(t, spools(t, e.dir), "nothing spooled after Close")
}

func TestStartupSweepRunsInTheBackground(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "attach-old"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other"), []byte("x"), 0o600))
	release := make(chan struct{})
	e := newEnv(t, func(o *Options) {
		o.Dir = dir
		o.sweepHook = func() { <-release }
	})
	e.b.setLive(1, false)
	// New returned while the sweep is held; the store works meanwhile.
	a, err := e.s.AddBytes(1, "c1", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	close(release)
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "attach-old"))
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, 5*time.Millisecond, "a previous run's spool is swept")
	assert.ElementsMatch(t, []string{"attach-" + a.ID, "other"}, spools(t, dir), "live spools and other files stay")
}

func TestOpenGivesTheFileOfThatServersAttachment(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddPath(1, "c1", e.file("pic.png", "PNGDATA"))
	require.NoError(t, err)
	b, err := e.s.AddBytes(1, "c1", "shot.png", "image/png", strings.NewReader("SPOOL"), 0)
	require.NoError(t, err)
	for id, want := range map[string]string{a.ID: "PNGDATA", b.ID: "SPOOL"} {
		f, info, err := e.s.Open(1, id)
		require.NoError(t, err)
		data, _ := io.ReadAll(f)
		_ = f.Close()
		assert.Equal(t, want, string(data))
		assert.Equal(t, "image/png", info.Mime)
	}
	_, _, err = e.s.Open(2, a.ID)
	assert.Equal(t, CodeNotFound, codeOf(err), "another server's id")
	_, _, err = e.s.Open(1, "nope")
	assert.Equal(t, CodeNotFound, codeOf(err))
}
