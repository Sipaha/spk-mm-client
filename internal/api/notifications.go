package api

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/state"
)

const (
	notifyBurst     = 10 * time.Second
	notifyBodyRunes = 200
)

type Notification struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ServerID  int64  `json:"server_id"`
	ChannelID string `json:"channel_id"`
}

// Notifier shows a notification; it may block (D-Bus), the service calls it
// from its own goroutine.
type Notifier interface{ Notify(Notification) }

// RecordingNotifier keeps notifications in memory (browser mode, tests).
type RecordingNotifier struct {
	mu   sync.Mutex
	list []Notification
}

func (r *RecordingNotifier) Notify(n Notification) {
	r.mu.Lock()
	r.list = append(r.list, n)
	r.mu.Unlock()
}

func (r *RecordingNotifier) List() []Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notification{}, r.list...)
}

func notificationFor(serverID int64, c state.NotifyCandidate) Notification {
	sender := c.SenderName
	if bool(c.Post.Props.FromWebhook) && c.Post.Props.OverrideUsername != "" {
		sender = string(c.Post.Props.OverrideUsername)
	}
	body := postText(c.Post)
	if c.Channel.Type != model.ChannelDirect {
		body = sender + ": " + body
	}
	return Notification{
		ID: fmt.Sprintf("mm-%d-%s", serverID, c.Post.ChannelID), Title: c.ChannelName,
		Body: truncateRunes(body, notifyBodyRunes), ServerID: serverID, ChannelID: c.Post.ChannelID,
	}
}

func postText(p model.Post) string {
	if t := strings.Join(strings.Fields(p.Message), " "); t != "" {
		return t
	}
	for _, a := range p.Props.Attachments {
		for _, s := range []string{a.Fallback, a.Pretext, a.Title, a.Text} {
			if t := strings.Join(strings.Fields(s), " "); t != "" {
				return t
			}
		}
	}
	if p.Metadata != nil && len(p.Metadata.Files) > 0 {
		return "📎 " + p.Metadata.Files[0].Name
	}
	if len(p.FileIDs) > 0 {
		return "📎"
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// notifyQueue shows the first notification of a channel at once and folds
// the rest of a burst (within window) into one "latest (+N)".
type notifyQueue struct {
	window time.Duration
	out    func(Notification)
	mu     sync.Mutex
	bursts map[string]*burst
	closed bool
}

type burst struct {
	extra  int
	latest Notification
	timer  *time.Timer
}

func newNotifyQueue(window time.Duration, out func(Notification)) *notifyQueue {
	return &notifyQueue{window: window, out: out, bursts: map[string]*burst{}}
}

func (q *notifyQueue) push(n Notification) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if b := q.bursts[n.ID]; b != nil {
		b.extra++
		b.latest = n
		return
	}
	b := &burst{}
	q.bursts[n.ID] = b
	b.timer = time.AfterFunc(q.window, func() { q.flush(n.ID, b) })
	go q.out(n)
}

func (q *notifyQueue) flush(id string, b *burst) {
	q.mu.Lock()
	if q.closed || q.bursts[id] != b {
		q.mu.Unlock()
		return
	}
	delete(q.bursts, id)
	extra, n := b.extra, b.latest
	q.mu.Unlock()
	if extra == 0 {
		return
	}
	if extra > 1 {
		n.Body = fmt.Sprintf("%s (+%d)", n.Body, extra-1)
	}
	q.out(n)
}

func (q *notifyQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for _, b := range q.bursts {
		b.timer.Stop()
	}
	q.bursts = map[string]*burst{}
}
