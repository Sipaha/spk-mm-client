package state

import (
	"math"
	"sync/atomic"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// NotificationReceipt follows a post's read watermark even after its burst
// has left the feed. It contains no message text or account credentials.
type NotificationReceipt struct {
	read *atomic.Int64
	at   int64
}

func (r *NotificationReceipt) Read() bool { return r != nil && r.read.Load() >= r.at }

type notificationWatch struct {
	read atomic.Int64
	at   int64
}

// WatchNotification tracks only groups that actually produced notifications.
// Channel reads cover replies without CRT; CRT replies need a thread read.
// Bound unread groups so an unattended desktop cannot retain them forever.
func (s *Server) WatchNotification(p model.Post) *NotificationReceipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notificationReads == nil {
		s.notificationReads = map[ComposerKey]*notificationWatch{}
	}
	key := ComposerKey{Channel: p.ChannelID}
	if s.crtLocked() {
		key.Root = p.RootID
	}
	w := s.notificationReads[key]
	if w == nil {
		if len(s.notificationReads) >= 512 {
			var oldest ComposerKey
			at := int64(math.MaxInt64)
			for k, v := range s.notificationReads {
				if v.at <= at {
					oldest, at = k, v.at
				}
			}
			s.notificationReads[oldest].read.Store(math.MaxInt64)
			delete(s.notificationReads, oldest)
		}
		w = &notificationWatch{}
		s.notificationReads[key] = w
	}
	w.at = max(w.at, p.CreateAt)
	if ch := s.chans[p.ChannelID]; ch == nil {
		w.read.Store(math.MaxInt64)
	} else if key.Root == "" {
		w.read.Store(max(w.read.Load(), ch.Member.LastViewedAt))
	} else if s.openThread == key.Root {
		w.read.Store(max(w.read.Load(), s.openRead.ts))
	}
	if w.read.Load() >= w.at {
		delete(s.notificationReads, key)
	}
	return &NotificationReceipt{read: &w.read, at: p.CreateAt}
}

func (s *Server) forgetNotificationsLocked(channel string) {
	for key := range s.notificationReads {
		if key.Channel == channel {
			s.readNotificationsLocked(key.Channel, key.Root, math.MaxInt64)
		}
	}
}

// readNotificationsLocked preserves a monotonic watermark: marking unread
// later must never resurrect an already dismissed system notification.
func (s *Server) readNotificationsLocked(channel, root string, at int64) {
	key := ComposerKey{Channel: channel, Root: root}
	if w := s.notificationReads[key]; w != nil {
		w.read.Store(max(w.read.Load(), at))
		if w.read.Load() >= w.at {
			delete(s.notificationReads, key)
		}
	}
}
