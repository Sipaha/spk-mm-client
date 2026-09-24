package mmfake

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/coder/websocket"
)

const deadQueueSize = 128

type wsEvent struct {
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
	Broadcast wsBroadcast    `json:"broadcast"`
	Seq       int64          `json:"seq"`
}

// wsSession is one server-side connection identity (connection_id). It
// outlives the socket: while parked (conn == nil) events keep landing in the
// dead queue, so a resume can replay them — like the real hub.
type wsSession struct {
	id      string
	userID  string
	nextSeq int64
	dead    []wsEvent
	conn    *websocket.Conn
	out     chan wsEvent
	cancel  context.CancelFunc
}

type wsHub struct{ sessions map[string]*wsSession }

func (h *wsHub) stamp(sess *wsSession, ev wsEvent) wsEvent {
	ev.Seq = sess.nextSeq
	sess.nextSeq++
	sess.dead = append(sess.dead, ev)
	if len(sess.dead) > deadQueueSize {
		sess.dead = sess.dead[len(sess.dead)-deadQueueSize:]
	}
	return ev
}

func (s *Server) websocketHandler(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.authed(r)
	if !ok {
		appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	q := r.URL.Query()
	connID, seqStr := q.Get("connection_id"), q.Get("sequence_number")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	s.mu.Lock()
	var sess *wsSession
	var replay []wsEvent
	hello := false
	if connID != "" {
		seq, perr := strconv.ParseInt(seqStr, 10, 64)
		if perr != nil {
			s.mu.Unlock()
			c.Close(websocket.StatusPolicyViolation, "sequence number not present")
			return
		}
		if old := s.hub.sessions[connID]; old != nil && old.userID == u.ID && old.conn == nil {
			sess = old
			idx := slices.IndexFunc(old.dead, func(e wsEvent) bool { return e.Seq == seq })
			switch {
			case idx >= 0:
				replay = append(replay, old.dead[idx:]...)
			case len(old.dead) == 0 || old.dead[len(old.dead)-1].Seq == seq-1:
				// lossless: nothing missed
			default:
				delete(s.hub.sessions, old.id)
				sess.id, sess.nextSeq, sess.dead = newID(), 0, nil
				hello = true
			}
		}
	}
	if sess == nil {
		sess = &wsSession{id: newID(), userID: u.ID}
		hello = true
	}
	s.hub.sessions[sess.id] = sess
	sess.conn, sess.out, sess.cancel = c, make(chan wsEvent, 1024), cancel
	out := sess.out
	if hello {
		out <- s.hub.stamp(sess, wsEvent{Event: "hello",
			Data:      map[string]any{"connection_id": sess.id, "server_version": "10.11.0-fake"},
			Broadcast: wsBroadcast{UserID: u.ID}})
	}
	for _, ev := range replay {
		out <- ev
	}
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-out:
				b, _ := json.Marshal(ev)
				if c.Write(ctx, websocket.MessageText, b) != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		// Reading also answers the client's pings.
		if _, _, err := c.Read(ctx); err != nil {
			break
		}
	}
	cancel()
	s.mu.Lock()
	if sess.conn == c {
		sess.conn, sess.out = nil, nil
	}
	s.mu.Unlock()
	_ = c.CloseNow()
}

// deliverLocked stamps the event into every session of every recipient
// (parked ones included) and pushes it to live sockets. A full socket queue
// drops that session entirely — the next resume then gets a new hello, the
// same as the real server's send-queue overflow.
func (s *Server) deliverLocked(name string, data map[string]any, b wsBroadcast, to []string, mentions []string) {
	for _, sess := range s.hub.sessions {
		if !slices.Contains(to, sess.userID) {
			continue
		}
		d := make(map[string]any, len(data)+1)
		for k, v := range data {
			d[k] = v
		}
		if slices.Contains(mentions, sess.userID) {
			d["mentions"] = `["` + sess.userID + `"]`
		}
		ev := s.hub.stamp(sess, wsEvent{Event: name, Data: d, Broadcast: b})
		if sess.out != nil {
			select {
			case sess.out <- ev:
			default:
				sess.cancel()
				sess.conn, sess.out = nil, nil
				delete(s.hub.sessions, sess.id)
			}
		}
	}
}

// DropConnections closes every socket. lose=true also forgets the sessions,
// so a resume gets a fresh connection_id (server restart / reaped conn).
func (s *Server) DropConnections(lose bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.hub.sessions {
		if sess.cancel != nil {
			sess.cancel()
		}
		sess.conn, sess.out = nil, nil
		if lose {
			delete(s.hub.sessions, id)
		}
	}
}
