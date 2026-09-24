package mmfake

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type frame struct {
	Event     string          `json:"event"`
	Data      map[string]any  `json:"data"`
	Broadcast json.RawMessage `json:"broadcast"`
	Seq       int64           `json:"seq"`
}

func dialWS(t *testing.T, s *Server, tok, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(s.URL(), "http") + "/api/v4/websocket" + query
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func read(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	require.NoError(t, err)
	var f frame
	require.NoError(t, json.Unmarshal(b, &f))
	return f
}

func noFrame(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := c.Read(ctx)
	require.Error(t, err, "expected no frame")
}

func TestWSHelloThenPostedWithRecipientMentions(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	hello := read(t, c)
	assert.Equal(t, "hello", hello.Event)
	assert.Equal(t, int64(0), hello.Seq)
	assert.NotEmpty(t, hello.Data["connection_id"])

	s.PostAs("c-offtopic", "bob", "hey @alice")
	f := read(t, c)
	assert.Equal(t, "posted", f.Event)
	assert.Equal(t, int64(1), f.Seq)
	assert.Equal(t, `["u-alice"]`, f.Data["mentions"])
	assert.IsType(t, "", f.Data["post"], "post is a JSON string")

	s.PostAs("c-offtopic", "bob", "no mention")
	f = read(t, c)
	assert.Equal(t, int64(2), f.Seq)
	_, has := f.Data["mentions"]
	assert.False(t, has)
}

func TestWSUnauthorizedRejected(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	u := "ws" + strings.TrimPrefix(s.URL(), "http") + "/api/v4/websocket"
	_, resp, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer nope"}}})
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestWSResumeReplaysMissed(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.PostAs("c-offtopic", "bob", "one")
	assert.Equal(t, int64(1), read(t, c).Seq)

	s.DropConnections(false)
	s.PostAs("c-offtopic", "bob", "two") // seq 2 while disconnected
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=2")
	f := read(t, c2)
	assert.Equal(t, "posted", f.Event, "replay, no hello")
	assert.Equal(t, int64(2), f.Seq)
}

func TestWSResumeLosslessSendsNothing(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.DropConnections(false)
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=1")

	// Not noFrame(t, c2): coder/websocket ties the context passed to Read to
	// the connection's read deadline (conn.go setupReadTimeout), so a Read
	// whose context actually expires closes the connection as a side effect
	// (verified against v1.8.14) - any later Read on c2 would then fail with
	// "use of closed network connection" instead of observing the live event
	// this test posts next. Read in the background with a live context and
	// race it against a timer instead, so "nothing arrived yet" doesn't
	// tear down the socket we still need.
	frames := make(chan frame, 1)
	go func() {
		_, b, err := c2.Read(context.Background())
		if err != nil {
			return
		}
		var f frame
		if json.Unmarshal(b, &f) == nil {
			frames <- f
		}
	}()
	select {
	case f := <-frames:
		t.Fatalf("expected no frame, got %+v", f)
	case <-time.After(200 * time.Millisecond):
	}

	s.PostAs("c-offtopic", "bob", "live")
	select {
	case f := <-frames:
		assert.Equal(t, int64(1), f.Seq)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for live frame")
	}
}

func TestWSResumeAfterLossGetsNewHello(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.DropConnections(true)
	s.PostAs("c-offtopic", "bob", "lost")
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=1")
	f := read(t, c2)
	assert.Equal(t, "hello", f.Event)
	assert.Equal(t, int64(0), f.Seq)
	assert.NotEqual(t, id, f.Data["connection_id"])
}

func TestWSOnlyMembersGetChannelEvents(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	carol := loginAs(t, s, "carol")
	c := dialWS(t, s, carol.tok, "")
	read(t, c) // hello
	s.PostAs("c-offtopic", "bob", "carol is not a member")
	noFrame(t, c)
}
