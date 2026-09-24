// Package ws is the Mattermost WebSocket client. It authenticates with the
// Authorization header on the upgrade request — the only way the server can
// resume a connection (the authentication_challenge path always gets a new
// connection_id) — tracks the event sequence, resumes with
// connection_id+sequence_number and detects dead peers with pings. See
// docs/research/2026-09-24-mattermost-api-facts.md §1.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/spk/spk-mattermost/internal/mm/rest"
)

const (
	defaultPingInterval = 30 * time.Second
	defaultPingTimeout  = 20 * time.Second
	readLimit           = 16 << 20
)

// eventBuffer is the Events() channel capacity; a var only so tests can
// force a full channel.
var eventBuffer = 4096

var ErrSeqGap = errors.New("ws: event sequence gap")

type Options struct {
	BaseURL      string // normalized http(s) server URL
	Token        string
	HTTPClient   *http.Client
	PingInterval time.Duration
	PingTimeout  time.Duration
}

// Resume identifies the server-side stream to continue: NextSeq is the next
// sequence number we expect (Mattermost's sequence_number semantics).
type Resume struct {
	ConnectionID string
	NextSeq      int64
}

type Broadcast struct {
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	TeamID    string `json:"team_id"`
}

type Event struct {
	Type      string
	Seq       int64
	Data      json.RawMessage
	Broadcast Broadcast
	// Reset: a hello that starts a new server-side stream after we already
	// had one — events were lost and the caller must resync.
	Reset bool
}

type Conn struct {
	ws     *websocket.Conn
	events chan Event
	cancel context.CancelFunc
	once   sync.Once

	mu     sync.Mutex
	resume Resume
	err    error
}

func URL(base string, r Resume) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("ws: unsupported scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v4/websocket"
	u.RawQuery = ""
	if r.ConnectionID != "" {
		u.RawQuery = "connection_id=" + url.QueryEscape(r.ConnectionID) + "&sequence_number=" + strconv.FormatInt(r.NextSeq, 10)
	}
	return u.String(), nil
}

func Dial(ctx context.Context, o Options, r Resume) (*Conn, error) {
	u, err := URL(o.BaseURL, r)
	if err != nil {
		return nil, &rest.Error{Kind: rest.KindAPI, Err: err}
	}
	wc, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPClient: o.HTTPClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + o.Token}},
	})
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, &rest.Error{Kind: rest.KindAuth, Status: resp.StatusCode, Err: err}
		}
		return nil, &rest.Error{Kind: rest.KindNetwork, Err: err}
	}
	wc.SetReadLimit(readLimit)
	interval, timeout := o.PingInterval, o.PingTimeout
	if interval <= 0 {
		interval = defaultPingInterval
	}
	if timeout <= 0 {
		timeout = defaultPingTimeout
	}
	rctx, cancel := context.WithCancel(context.Background())
	c := &Conn{ws: wc, events: make(chan Event, eventBuffer), cancel: cancel, resume: r}
	go c.readLoop(rctx)
	go c.keepalive(rctx, interval, timeout)
	return c, nil
}

func (c *Conn) Events() <-chan Event { return c.events }

// Resume is where a new Dial should continue the stream. It counts as
// delivered exactly the events already placed in the Events() channel
// (received by the caller or still buffered there): NextSeq is one past the
// last of them, and ConnectionID is that of the last hello placed there.
// An event read from the socket but dropped because the Conn was closed
// while the channel was full is not counted, so a resume replays it. The
// value is final once Events() is closed; before that it may lag by the one
// event being handed over (a resume would then replay it, never skip it).
func (c *Conn) Resume() Resume {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resume
}

// Err is why the stream ended; meaningful once Events() is closed.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Close() {
	c.fail(errors.New("closed"))
}

// fail records the first error and tears the socket down; the read loop
// then closes Events().
func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = &rest.Error{Kind: rest.KindNetwork, Err: err}
	}
	c.mu.Unlock()
	c.once.Do(func() {
		c.cancel()
		_ = c.ws.CloseNow()
	})
}

type envelope struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	Broadcast Broadcast       `json:"broadcast"`
	Seq       int64           `json:"seq"`
}

func (c *Conn) readLoop(ctx context.Context) {
	defer close(c.events)
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			c.fail(fmt.Errorf("ws read: %w", err))
			return
		}
		var env envelope
		if json.Unmarshal(b, &env) != nil || env.Event == "" {
			continue // replies to our requests (seq_reply), junk
		}
		ev := Event{Type: env.Event, Seq: env.Seq, Data: env.Data, Broadcast: env.Broadcast}
		// readLoop is the only writer of c.resume; the lock is for readers.
		c.mu.Lock()
		cur := c.resume
		c.mu.Unlock()
		var nr Resume
		if env.Event == "hello" {
			var d struct {
				ConnectionID string `json:"connection_id"`
			}
			_ = json.Unmarshal(env.Data, &d)
			ev.Reset = cur.ConnectionID != "" && d.ConnectionID != cur.ConnectionID
			nr = Resume{ConnectionID: d.ConnectionID, NextSeq: env.Seq + 1}
		} else if env.Seq != cur.NextSeq {
			// Keep NextSeq at the gap: the resume asks the server for it.
			c.fail(fmt.Errorf("%w: got %d, want %d", ErrSeqGap, env.Seq, cur.NextSeq))
			return
		} else {
			nr = Resume{ConnectionID: cur.ConnectionID, NextSeq: env.Seq + 1}
		}
		// Advance the resume point only once the event is in the channel:
		// an event dropped on close must be replayed by the next resume.
		select {
		case c.events <- ev:
		case <-ctx.Done():
			return
		}
		c.mu.Lock()
		c.resume = nr
		c.mu.Unlock()
	}
}

// keepalive pings the server; a socket that stays open while the peer stops
// answering (dropped NAT mapping, hung proxy, laptop sleep) would otherwise
// look live forever.
func (c *Conn) keepalive(ctx context.Context, interval, timeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, timeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					c.fail(fmt.Errorf("ws keepalive: %w", err))
				}
				return
			}
		}
	}
}
