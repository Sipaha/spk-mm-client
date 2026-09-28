package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmfake"
)

func login(t *testing.T, f *mmfake.Server, user string) string {
	t.Helper()
	tok, _, err := rest.New(f.URL(), "", nil).Login(context.Background(), user, "secret")
	require.NoError(t, err)
	return tok
}

func next(t *testing.T, c *Conn) Event {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		require.True(t, ok, "stream closed: %v", c.Err())
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func waitClosed(t *testing.T, c *Conn) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream did not close")
		}
	}
}

func TestURL(t *testing.T) {
	u, err := URL("https://mm.example.com/sub", Resume{})
	require.NoError(t, err)
	assert.Equal(t, "wss://mm.example.com/sub/api/v4/websocket", u)
	u, _ = URL("http://127.0.0.1:8065", Resume{ConnectionID: "abc", NextSeq: 7})
	assert.Equal(t, "ws://127.0.0.1:8065/api/v4/websocket?connection_id=abc&sequence_number=7", u)
}

func TestDialHelloAndPosted(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	c, err := Dial(context.Background(), Options{BaseURL: f.URL(), Token: login(t, f, "alice")}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	hello := next(t, c)
	assert.Equal(t, "hello", hello.Type)
	assert.False(t, hello.Reset)
	// The resume point advances only after the event is placed in Events()
	// (so a resume never skips one we have not taken): wait for it.
	require.Eventually(t, func() bool { return c.Resume().NextSeq == 1 }, 2*time.Second, time.Millisecond, "NextSeq after hello")
	assert.NotEmpty(t, c.Resume().ConnectionID)

	f.PostAs("c-offtopic", "bob", "hi @alice")
	ev := next(t, c)
	require.Equal(t, "posted", ev.Type)
	p, err := DecodePosted(ev)
	require.NoError(t, err)
	assert.Equal(t, "hi @alice", p.Post.Message)
	assert.Equal(t, []string{"u-alice"}, p.Mentions)
	assert.Equal(t, "c-offtopic", ev.Broadcast.ChannelID)
	require.Eventually(t, func() bool { return c.Resume().NextSeq == 2 }, 2*time.Second, time.Millisecond, "NextSeq after posted")
}

func TestDialUnauthorizedIsAuthError(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	_, err := Dial(context.Background(), Options{BaseURL: f.URL(), Token: "bad"}, Resume{})
	require.Error(t, err)
	assert.True(t, rest.IsAuth(err))
}

func TestResumeReplaysMissedEvents(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	o := Options{BaseURL: f.URL(), Token: login(t, f, "alice")}
	c, err := Dial(context.Background(), o, Resume{})
	require.NoError(t, err)
	next(t, c) // hello
	f.DropConnections(false)
	waitClosed(t, c)
	assert.True(t, rest.IsNetwork(c.Err()))
	f.PostAs("c-offtopic", "bob", "while away")

	c2, err := Dial(context.Background(), o, c.Resume())
	require.NoError(t, err)
	defer c2.Close()
	ev := next(t, c2)
	require.Equal(t, "posted", ev.Type, "replayed, no hello")
	p, _ := DecodePosted(ev)
	assert.Equal(t, "while away", p.Post.Message)
}

func TestResumeAfterLossGetsResetHello(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	o := Options{BaseURL: f.URL(), Token: login(t, f, "alice")}
	c, err := Dial(context.Background(), o, Resume{})
	require.NoError(t, err)
	next(t, c)
	old := c.Resume().ConnectionID
	f.DropConnections(true)
	waitClosed(t, c)
	c2, err := Dial(context.Background(), o, c.Resume())
	require.NoError(t, err)
	defer c2.Close()
	hello := next(t, c2)
	assert.Equal(t, "hello", hello.Type)
	assert.True(t, hello.Reset)
	assert.NotEqual(t, old, c2.Resume().ConnectionID)
}

// rawServer runs a hand-written server side for protocol edge cases.
func rawServer(t *testing.T, h func(ctx context.Context, c *websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		h(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSeqGapClosesAndKeepsResumePoint(t *testing.T) {
	u := rawServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"hello","data":{"connection_id":"x"},"seq":0}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"posted","data":{},"seq":5}`))
		_, _, _ = c.Read(ctx)
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t"}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	assert.Equal(t, "hello", next(t, c).Type)
	waitClosed(t, c)
	assert.True(t, errors.Is(c.Err(), ErrSeqGap))
	assert.Equal(t, Resume{ConnectionID: "x", NextSeq: 1}, c.Resume())
}

func TestRepliesWithSeqReplyAreIgnored(t *testing.T) {
	u := rawServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"hello","data":{"connection_id":"x"},"seq":0}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"status":"OK","seq_reply":1}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"typing","data":{},"seq":1}`))
		_, _, _ = c.Read(ctx)
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t"}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	next(t, c)
	assert.Equal(t, "typing", next(t, c).Type)
}

func TestKeepaliveDetectsSilentPeer(t *testing.T) {
	u := rawServer(t, func(ctx context.Context, _ *websocket.Conn) {
		<-ctx.Done() // never reads → never answers pings
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t", PingInterval: 50 * time.Millisecond, PingTimeout: 100 * time.Millisecond}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	waitClosed(t, c)
	assert.True(t, rest.IsNetwork(c.Err()))
}

func TestCloseIsIdempotentAndClosesEvents(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	c, err := Dial(context.Background(), Options{BaseURL: f.URL(), Token: login(t, f, "alice")}, Resume{})
	require.NoError(t, err)
	c.Close()
	c.Close()
	waitClosed(t, c)
}

// An event read while the consumer is not draining and dropped by Close must
// not be counted in Resume: NextSeq stays one past the last event actually
// placed in Events().
func TestCloseWhileBlockedDoesNotSkipUndeliveredEvent(t *testing.T) {
	old := eventBuffer
	eventBuffer = 1
	t.Cleanup(func() { eventBuffer = old })
	sent := make(chan struct{})
	u := rawServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"hello","data":{"connection_id":"x"},"seq":0}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"posted","data":{},"seq":1}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"posted","data":{},"seq":2}`))
		close(sent)
		_, _, _ = c.Read(ctx)
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t"}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	assert.Equal(t, "hello", next(t, c).Type)
	// Stop draining: seq 1 fills the buffer, the read loop blocks sending seq 2.
	<-sent
	require.Eventually(t, func() bool { return len(c.events) == 1 }, 5*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond) // let the read loop reach the blocked send of seq 2
	c.Close()

	last := int64(0) // hello
	deadline := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				done = true
				break
			}
			last = ev.Seq
		case <-deadline:
			t.Fatal("stream did not close")
		}
	}
	assert.Equal(t, Resume{ConnectionID: "x", NextSeq: last + 1}, c.Resume())
}
