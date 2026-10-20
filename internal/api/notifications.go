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
	// RootID: a reply's thread — the click opens it after the channel.
	RootID string `json:"root_id,omitempty"`
	// Internal read receipt and worker identity survive delayed burst delivery.
	// Neither is exposed to the UI or the OS notification payload.
	receipt *state.NotificationReceipt
	owner   *state.Server
}

// Notifier shows a notification; it may block (D-Bus), the service calls it
// from its own goroutine.
type Notifier interface{ Notify(Notification) }

// NotificationRemover optionally retracts a delivered OS notification.
// Like the desktop Notify implementation, it must queue work without waiting
// for the OS: reconciliation runs on the service's coalescer.
type NotificationRemover interface{ RemoveNotification(string) }

// Read reports a confirmed read, including one received after this item
// entered the desktop delivery queue.
func (n Notification) Read() bool { return n.receipt != nil && n.receipt.Read() }

func (s *Service) notificationCurrent(n Notification) bool {
	if n.receipt == nil { // hand-built/test notifications have no receipt
		return true
	}
	m := s.manager()
	if m == nil || n.receipt.Read() {
		return false
	}
	w := m.Worker(n.ServerID)
	return w != nil && w.State() == n.owner
}

// reconcileNotifications is also called for read changes that leave the
// aggregate badge unchanged (another channel/server may still be unread).
func (s *Service) reconcileNotifications() {
	s.notificationsMu.Lock()
	defer s.notificationsMu.Unlock()
	s.mu.Lock()
	nt := s.notifier
	s.mu.Unlock()
	remover, ok := nt.(NotificationRemover)
	if !ok {
		return
	}
	for id, n := range s.deliveredNotifications {
		if !s.notificationCurrent(n) {
			remover.RemoveNotification(id)
			delete(s.deliveredNotifications, id)
		}
	}
}

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
	sender := c.SenderName // state's author: a webhook's name only where the server allows it
	body := postText(c.Post)
	if c.Channel.Type != model.ChannelDirect {
		body = sender + ": " + body
	}
	n := Notification{
		ID: fmt.Sprintf("mm-%d-%s", serverID, c.Post.ChannelID), Title: c.ChannelName,
		Body: truncateRunes(body, notifyBodyRunes), ServerID: serverID, ChannelID: c.Post.ChannelID,
	}
	if r := c.Post.RootID; r != "" {
		// A reply (either mode) opens its thread; a thread's series folds
		// apart from its channel's (notifyQueue keys by ID).
		n.ID, n.RootID = n.ID+"-"+r, r
	}
	return n
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
