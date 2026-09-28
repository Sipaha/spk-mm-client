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

func (c *changes) add(_ int64, ch, root string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, ch+"|"+root)
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
	a, err := e.s.AddPath(1, "c1", "", ok)
	require.NoError(t, err)
	assert.Equal(t, "notes.txt", a.Name)
	assert.Equal(t, int64(5), a.Size)
	assert.Equal(t, "text/plain", a.Mime)
	assert.Equal(t, StateStaged, a.State)
	assert.Regexp(t, `^[a-z0-9]{26}$`, a.ID, "an id the server accepts as client_id and the media route as a key")

	_, err = e.s.AddPath(1, "c1", "", filepath.Dir(ok))
	assert.Equal(t, CodeNotAFile, codeOf(err), "a directory")
	_, err = e.s.AddPath(1, "c1", "", filepath.Join(filepath.Dir(ok), "missing"))
	assert.Equal(t, CodeNotAFile, codeOf(err), "a missing file")
	_, err = e.s.AddPath(1, "c1", "", "notes.txt")
	assert.Equal(t, CodeNotAFile, codeOf(err), "a relative path")
	_, err = e.s.AddPath(1, "c1", "", e.file("big.bin", strings.Repeat("x", 1<<20+1)))
	assert.Equal(t, CodeTooLarge, codeOf(err))
	_, err = e.s.AddPath(9, "c1", "", ok)
	assert.ErrorIs(t, err, errNotSignedIn, "the backend's error passes through")

	e.b.mu.Lock()
	e.b.limits[2] = Limits{Enabled: false, MaxFileSize: 1 << 20}
	e.b.mu.Unlock()
	_, err = e.s.AddPath(2, "c1", "", ok)
	assert.Equal(t, CodeDisabled, codeOf(err))

	e.b.mu.Lock()
	e.b.limits[2] = Limits{} // config not read yet: the server decides
	e.b.mu.Unlock()
	_, err = e.s.AddPath(2, "c1", "", e.file("big2.bin", strings.Repeat("x", 1<<20+1)))
	assert.NoError(t, err, "an unknown config refuses nothing")

	assert.Len(t, e.s.List(1, "c1", ""), 1)
	assert.Empty(t, e.s.List(1, "c2", ""))
	assert.NotNil(t, e.s.List(1, "c2", ""), "an empty list, not null, in JSON")
}

func TestAtMostTenPerChannel(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	p := e.file("a.txt", "a")
	for i := 0; i < MaxPerChannel; i++ {
		_, err := e.s.AddPath(1, "c1", "", p)
		require.NoError(t, err)
	}
	_, err := e.s.AddPath(1, "c1", "", p)
	assert.Equal(t, CodeTooMany, codeOf(err))
	_, err = e.s.AddBytes(1, "c1", "", "x.png", "image/png", strings.NewReader("x"), 0)
	assert.Equal(t, CodeTooMany, codeOf(err))
	assert.Empty(t, spools(t, e.dir), "a refused byte upload leaves no spool")
	_, err = e.s.AddPath(1, "c2", "", p)
	assert.NoError(t, err, "another channel has its own ten")
}

func TestAddBytesSpoolsUnderTheLimit(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddBytes(1, "c1", "", "../../Screenshot.png", "", bytes.NewReader(pngHeader()), 0)
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

	_, err = e.s.AddBytes(1, "c1", "", "big.bin", "application/octet-stream", strings.NewReader(strings.Repeat("x", 11)), 10)
	assert.Equal(t, CodeTooLarge, codeOf(err), "over the caller's limit")
	_, err = e.s.AddBytes(1, "c1", "", "big.bin", "application/octet-stream", strings.NewReader(strings.Repeat("x", 1<<20+1)), 0)
	assert.Equal(t, CodeTooLarge, codeOf(err), "over the server's limit")
	assert.Len(t, spools(t, e.dir), 1, "refused bodies are not left on disk")

	a2, err := e.s.AddBytes(1, "c1", "", "", "text/plain; charset=utf-8", strings.NewReader("hi"), 0)
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
	a, err := e.s.AddPath(1, "c1", "", e.file("data.bin", strings.Repeat("y", 1000))) // 100 chunks, ~200+ ms
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
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "the content"))
	require.NoError(t, err)
	e.waitState(a.ID, StateUploaded)
	assert.Equal(t, "the content", string(e.up.body(a.ID)))
	b, err := e.s.AddBytes(1, "c1", "", "y.txt", "text/plain", strings.NewReader("spooled"), 0)
	require.NoError(t, err)
	e.waitState(b.ID, StateUploaded)
	assert.Equal(t, "spooled", string(e.up.body(b.ID)))
}

func TestChangedOrMissingFileFailsItsUpload(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	p := e.file("x.txt", "12345")
	a, err := e.s.AddPath(1, "c1", "", p)
	require.NoError(t, err)
	gone := e.file("y.txt", "abc")
	b, err := e.s.AddPath(1, "c1", "", gone)
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
	c, err := e.s.AddPath(1, "c1", "", p2)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(p2, time.Now(), time.Now().Add(time.Hour)))
	e.b.setLive(1, true)
	e.s.Wake(1)
	assert.Equal(t, CodeChanged, e.waitState(c.ID, StateFailed).Error)
}

func TestOfflineAttachmentsWaitForLive(t *testing.T) {
	e := newEnv(t)
	a, err := e.s.AddPath(2, "c1", "", e.file("x.txt", "abc"))
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
	a, err := e.s.AddBytes(1, "c1", "", "x.bin", "application/octet-stream", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	<-started
	assert.Equal(t, StateUploading, e.waitState(a.ID, StateUploading).State)
	require.NoError(t, e.s.Remove(a.ID))
	_, ok := e.state(a.ID)
	assert.False(t, ok)
	assert.Empty(t, e.s.List(1, "c1", ""))
	assert.Empty(t, spools(t, e.dir))
	require.Eventually(t, func() bool { return e.up.running.Load() == 0 }, 5*time.Second, 5*time.Millisecond, "the request was not cancelled")
	assert.Equal(t, CodeNotFound, codeOf(e.s.Remove(a.ID)))
}

func TestUnauthorizedFailsAndAsksForSignInWithoutRetrying(t *testing.T) {
	e := newEnv(t)
	e.up.setSend(func(context.Context, io.Reader, int64, func(int64)) error {
		return &rest.Error{Kind: rest.KindAuth, Status: http.StatusUnauthorized, Err: errors.New("401")}
	})
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
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
		a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
		require.NoError(t, err)
		assert.Equal(t, tc.code, e.waitState(a.ID, StateFailed).Error, "%v", tc.err)
	}
}

func TestStalledUploadFails(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Stall = 80 * time.Millisecond })
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
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
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abcdefghij"))
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
		a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
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
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
	require.NoError(t, err)
	b, err := e.s.AddPath(1, "c1", "", e.file("y.txt", "abc"))
	require.NoError(t, err)
	type result struct {
		ids []string
		err error
	}
	res := make(chan result, 1)
	go func() {
		ids, err := e.s.Wait(context.Background(), []string{b.ID, a.ID})
		res <- result{ids, err}
	}()
	select {
	case r := <-res:
		t.Fatalf("returned while staged: %v", r.err)
	case <-time.After(30 * time.Millisecond):
	}
	e.b.setLive(1, true)
	e.s.Wake(1)
	r := <-res
	require.NoError(t, r.err)
	assert.Equal(t, []string{"f-" + b.ID, "f-" + a.ID}, r.ids, "the server file ids, in the order asked")

	e.up.setSend(func(context.Context, io.Reader, int64, func(int64)) error {
		return &rest.Error{Kind: rest.KindNetwork, Err: errors.New("down")}
	})
	c, err := e.s.AddPath(1, "c1", "", e.file("z.txt", "abc"))
	require.NoError(t, err)
	_, err = e.s.Wait(context.Background(), []string{a.ID, c.ID})
	assert.Equal(t, CodeUnreachable, codeOf(err))
	require.NoError(t, e.s.Remove(c.ID))
	_, err = e.s.Wait(context.Background(), []string{c.ID})
	assert.Equal(t, CodeNotFound, codeOf(err))

	e.b.setLive(1, false)
	d, err := e.s.AddPath(1, "c1", "", e.file("w.txt", "abc"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = e.s.Wait(ctx, []string{d.ID})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTakeMovesAttachmentsFromTheComposerToAPost(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddBytes(1, "c1", "", "shot.png", "image/png", strings.NewReader("png bytes"), 0)
	require.NoError(t, err)
	b, err := e.s.AddPath(1, "c1", "", e.file("b.txt", "abc"))
	require.NoError(t, err)
	other, err := e.s.AddPath(1, "c2", "", e.file("o.txt", "abc"))
	require.NoError(t, err)

	// All or nothing: another channel's, another server's, unknown ids.
	_, err = e.s.Take(1, "c1", "", []string{a.ID, other.ID})
	assert.Equal(t, CodeNotFound, codeOf(err))
	_, err = e.s.Take(2, "c1", "", []string{a.ID})
	assert.Equal(t, CodeNotFound, codeOf(err))
	_, err = e.s.Take(1, "c1", "", []string{a.ID, "nope"})
	assert.Equal(t, CodeNotFound, codeOf(err))
	require.Len(t, e.s.List(1, "c1", ""), 2, "nothing taken by a refused Take")

	before := e.chg.count()
	got, err := e.s.Take(1, "c1", "", []string{b.ID, a.ID, b.ID})
	require.NoError(t, err)
	require.Len(t, got, 2, "a repeated id is taken once")
	assert.Equal(t, []string{b.ID, a.ID}, []string{got[0].ID, got[1].ID}, "in the order given")
	assert.True(t, got[0].Taken && got[1].Taken)
	assert.Equal(t, "shot.png", got[1].Name)
	assert.Empty(t, e.s.List(1, "c1", ""), "gone from the composer")
	assert.Greater(t, e.chg.count(), before, "the composer is told")
	_, err = e.s.Take(1, "c1", "", []string{a.ID})
	assert.Equal(t, CodeNotFound, codeOf(err), "taken once only")

	// Taken ones do not count against the composer's limit.
	for i := 0; i < MaxPerChannel; i++ {
		_, err := e.s.AddBytes(1, "c1", "", "x.txt", "", strings.NewReader("x"), 0)
		require.NoError(t, err)
	}

	// They stay alive for the post: uploaded, waited for, and their spool
	// is kept until they are removed.
	assert.Contains(t, spools(t, e.dir), spoolPrefix+a.ID)
	e.b.setLive(1, true)
	e.s.Wake(1)
	ids, err := e.s.Wait(context.Background(), []string{a.ID, b.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"f-" + a.ID, "f-" + b.ID}, ids)
	pic, ok := e.s.Get(a.ID)
	require.True(t, ok)
	assert.True(t, pic.Taken)
	require.NoError(t, e.s.Remove(a.ID))
	assert.NotContains(t, spools(t, e.dir), spoolPrefix+a.ID)
	assert.Len(t, e.s.List(1, "c1", ""), MaxPerChannel, "removing a taken one leaves the composer alone")
}

// Task 4: attachments are keyed by (server, channel, root) — a channel's
// composer ("" root) and a thread's reply composer are separate lists that
// never see each other's ids, and Take (which SendPost/SendReply use to
// move them onto the post being sent) enforces the same boundary.
func TestThreadAttachmentsAreNotSentToTheChannel(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	chanFile, err := e.s.AddPath(1, "c1", "", e.file("chan.txt", "abc"))
	require.NoError(t, err)
	replyFile, err := e.s.AddPath(1, "c1", "r1", e.file("reply.txt", "abc"))
	require.NoError(t, err)

	// Listed separately: the channel's composer never sees the reply's
	// attachment, and the reverse.
	assert.Equal(t, []Attachment{chanFile}, e.s.List(1, "c1", ""))
	assert.Equal(t, []Attachment{replyFile}, e.s.List(1, "c1", "r1"))

	// The channel's Take cannot carry off the thread's attachment...
	_, err = e.s.Take(1, "c1", "", []string{replyFile.ID})
	assert.Equal(t, CodeNotFound, codeOf(err), "a thread's attachment is not the channel's to send")
	// ...and the reverse.
	_, err = e.s.Take(1, "c1", "r1", []string{chanFile.ID})
	assert.Equal(t, CodeNotFound, codeOf(err), "the channel's attachment is not this thread's to send")
	// Neither Take moved anything: both composers are unchanged.
	assert.Equal(t, []Attachment{chanFile}, e.s.List(1, "c1", ""))
	assert.Equal(t, []Attachment{replyFile}, e.s.List(1, "c1", "r1"))

	// A second thread on the same channel is its own list too.
	other, err := e.s.AddPath(1, "c1", "r2", e.file("other-reply.txt", "abc"))
	require.NoError(t, err)
	assert.Equal(t, []Attachment{other}, e.s.List(1, "c1", "r2"))
	assert.Len(t, e.s.List(1, "c1", "r1"), 1, "r1's list is unaffected by r2")

	// Take with the right (channel, root) works as usual.
	got, err := e.s.Take(1, "c1", "r1", []string{replyFile.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, e.s.List(1, "c1", "r1"), "gone from the thread's composer")
	assert.Equal(t, []Attachment{chanFile}, e.s.List(1, "c1", ""), "the channel's composer untouched")
}

func TestPauseReturnsRunningUploadsToTheQueue(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
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

// Fix round 1 (review item 5): a thread's reply composer left behind
// (channel left, taking its held threads with it — internal/state
// TakeForgottenComposers) can still be sitting on staged attachments and
// their spools; ReleaseComposer drops those, but never something already
// mid-upload (left to finish or fail on its own — a channel-leave race is
// rare and the alternative, cancelling an in-flight PUT, is worse).
func TestReleaseComposerDropsStagedNotUploading(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Parallel = 1 })
	started := make(chan struct{}, 1)
	e.up.setSend(blockUntilCancelled(started))
	up, err := e.s.AddPath(1, "c1", "r1", e.file("up.bin", "abc"))
	require.NoError(t, err)
	<-started
	e.waitState(up.ID, StateUploading)

	// Parallel=1: this one stays queued (StateStaged), not started.
	staged, err := e.s.AddBytes(1, "c1", "r1", "staged.bin", "", strings.NewReader("xyz"), 0)
	require.NoError(t, err)
	other, err := e.s.AddPath(1, "c1", "", e.file("other.bin", "qqq")) // the channel's own composer
	require.NoError(t, err)
	require.Contains(t, spools(t, e.dir), spoolPrefix+staged.ID)

	before := e.chg.count()
	e.s.ReleaseComposer(1, "c1", "r1")

	_, stillStaged := e.state(staged.ID)
	assert.False(t, stillStaged, "the staged attachment is gone")
	assert.NotContains(t, spools(t, e.dir), spoolPrefix+staged.ID, "its spool is deleted too")
	stillUp, ok := e.state(up.ID)
	require.True(t, ok, "the uploading one is left alone")
	assert.Equal(t, StateUploading, stillUp.State)
	_, otherOK := e.state(other.ID)
	assert.True(t, otherOK, "a different composer (the channel's own) is untouched")
	assert.Greater(t, e.chg.count(), before, "the composer's OnChange fires")

	// Nothing left to release, and an unknown composer: both no-ops, not
	// a panic.
	e.s.ReleaseComposer(1, "c1", "r1")
	e.s.ReleaseComposer(9, "nope", "nope")
}

func TestDropServerForgetsItsAttachments(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddBytes(1, "c1", "", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	b, err := e.s.AddBytes(2, "c1", "", "y.bin", "", strings.NewReader("abc"), 0)
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
	_, err := e.s.AddBytes(1, "c1", "", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	<-started
	e.s.Close()
	assert.Zero(t, e.up.running.Load(), "Close waits for cancelled uploads")
	assert.Empty(t, spools(t, e.dir))
	_, err = e.s.AddBytes(1, "c1", "", "x.bin", "", strings.NewReader("abc"), 0)
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
	a, err := e.s.AddBytes(1, "c1", "", "x.bin", "", strings.NewReader("abc"), 0)
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
	a, err := e.s.AddPath(1, "c1", "", e.file("pic.png", "PNGDATA"))
	require.NoError(t, err)
	b, err := e.s.AddBytes(1, "c1", "", "shot.png", "image/png", strings.NewReader("SPOOL"), 0)
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

func TestEmptyFilesAreRefused(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	_, err := e.s.AddPath(1, "c1", "", e.file("empty.txt", ""))
	assert.Equal(t, CodeEmptyFile, codeOf(err), "the server refuses Content-Length 0")
	_, err = e.s.AddBytes(1, "c1", "", "empty.png", "image/png", strings.NewReader(""), 0)
	assert.Equal(t, CodeEmptyFile, codeOf(err))
	assert.Empty(t, spools(t, e.dir), "no spool left")
	assert.Empty(t, e.s.List(1, "c1", ""))
}

func TestUnknownConfigBoundsSizesByTheDefault(t *testing.T) {
	assert.Equal(t, int64(100<<20), int64(DefaultMaxFileSize), "the server's default MaxFileSize")
	e := newEnv(t)
	e.b.setLive(2, false)
	e.b.mu.Lock()
	e.b.limits[2] = Limits{} // config not read yet
	e.b.mu.Unlock()
	big := filepath.Join(t.TempDir(), "sparse.bin")
	f, err := os.Create(big)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(DefaultMaxFileSize+1)) // sparse: nothing written
	require.NoError(t, f.Close())
	_, err = e.s.AddPath(2, "c1", "", big)
	assert.Equal(t, CodeTooLarge, codeOf(err))

	e2 := newEnv(t, func(o *Options) { o.unknownMax = 10 })
	e2.b.mu.Lock()
	e2.b.limits[2] = Limits{}
	e2.b.mu.Unlock()
	_, err = e2.s.AddBytes(2, "c1", "", "x.bin", "", strings.NewReader(strings.Repeat("x", 11)), 0)
	assert.Equal(t, CodeTooLarge, codeOf(err), "the spool is bounded too")
	assert.Empty(t, spools(t, e2.dir))
	_, err = e2.s.AddBytes(2, "c1", "", "x.bin", "", strings.NewReader(strings.Repeat("x", 10)), 0)
	assert.NoError(t, err, "Enabled is not held against an unknown config")
}

func TestAddPathSniffsNamesWithoutAKnownExtension(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddPath(1, "c1", "", e.file("screenshot", string(pngHeader())))
	require.NoError(t, err)
	assert.Equal(t, "image/png", a.Mime)
	b, err := e.s.AddPath(1, "c1", "", e.file("blob.weird", "\x00\x01\x02"))
	require.NoError(t, err)
	assert.Equal(t, "application/octet-stream", b.Mime)
}

func TestPauseRequeuesInTheOrderAdded(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Parallel = 8 })
	started := make(chan struct{}, 8)
	e.up.setSend(blockUntilCancelled(started))
	var ids []string
	for i := 0; i < 8; i++ {
		a, err := e.s.AddPath(1, "c"+string(rune('a'+i%3)), "", e.file("x.txt", "abc"))
		require.NoError(t, err)
		ids = append(ids, a.ID)
	}
	for range ids {
		<-started
	}
	e.s.Pause(1)
	e.s.mu.Lock()
	var got []string
	for _, it := range e.s.queues[1].pending {
		got = append(got, it.ID)
	}
	e.s.mu.Unlock()
	assert.Equal(t, ids, got)
}

// switchingBackend: the first Uploader call hands out the old worker's
// uploader, but the worker is stopped (Pause) and a Wake comes while it
// does (the new worker is not live yet then); later calls give the new
// worker's uploader.
type switchingBackend struct {
	*fakeBackend
	s        *Store
	old, cur *fakeUploader
	mu       sync.Mutex
	calls    int
}

func (b *switchingBackend) Uploader(srv int64) Uploader {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.mu.Unlock()
	switch n {
	case 1:
		b.s.Pause(srv)
		b.s.Wake(srv)
		return b.old
	case 2:
		return nil // the Wake above: not live yet
	}
	return b.cur
}

func TestAStaleUploaderIsNotUsedAfterPauseAndWake(t *testing.T) {
	e := newEnv(t)
	sb := &switchingBackend{fakeBackend: e.b, old: &fakeUploader{}, cur: &fakeUploader{}}
	e.s.o.Backend = sb
	sb.s = e.s
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	e.s.Wake(1) // the new worker is live
	e.waitState(a.ID, StateUploaded)
	assert.Zero(t, sb.old.callCount(), "the stopped worker's uploader was never used")
	assert.Equal(t, 1, sb.cur.callCount())
}

func TestPauseAgainUndoesAWakeFromTheStoppingWorker(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{}, 2)
	e.up.setSend(blockUntilCancelled(started))
	a, err := e.s.AddPath(1, "c1", "", e.file("x.txt", "abc"))
	require.NoError(t, err)
	<-started
	e.s.Pause(1) // before the worker stops
	e.s.Wake(1)  // the old worker went live again meanwhile
	<-started    // … and got the upload
	e.s.Pause(1) // after the worker stopped
	e.waitState(a.ID, StateStaged)
	require.Eventually(t, func() bool { return e.up.running.Load() == 0 }, 5*time.Second, 5*time.Millisecond)
	e.up.setSend(nil)
	e.s.Wake(1)
	e.waitState(a.ID, StateUploaded)
}
