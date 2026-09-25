package api

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/notify"
	"github.com/spk/spk-mm-client/internal/state"
	"github.com/spk/spk-mm-client/internal/store"
)

const coalesceDelay = 100 * time.Millisecond

type serverMark struct {
	status mmsync.Status
	badge  Badge
}

// Start launches a sync worker for every signed-in server. It only reads the
// local database — network work happens in the workers' goroutines — so it
// never delays application startup. Calling it again replaces the manager.
func (s *Service) Start(ctx context.Context) error {
	cfg := mmsync.Config{Store: s.st, HTTPClient: s.hc, Hooks: mmsync.Hooks{
		Changed: s.onChanged, Status: s.onStatus, Notify: s.onNotify,
	}}
	if s.tune != nil {
		s.tune(&cfg)
	}
	m := mmsync.NewManager(context.Background(), cfg)
	s.mu.Lock()
	old := s.mgr
	s.mgr = m
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return m.StartAll(ctx)
}

// Close stops every worker (each flushes its snapshot) and drops pending UI
// events and notifications.
func (s *Service) Close() {
	if m := s.manager(); m != nil {
		m.Close()
	}
	s.co.Close()
	s.nq.close()
}

func (s *Service) manager() *mmsync.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mgr
}

// OnBadge registers fn for the badge summed over all servers; it is called
// from a background goroutine whenever the sum changes (tray).
//
// fn is called at once, synchronously, with the current total: a subscriber
// registered after startup (the tray) would otherwise miss the badge restored
// from the cache, since later calls happen only when the total changes.
func (s *Service) OnBadge(fn func(Badge)) {
	s.badgeMu.Lock()
	defer s.badgeMu.Unlock()
	s.mu.Lock()
	s.badgeFns = append(s.badgeFns, fn)
	total := s.total
	s.mu.Unlock()
	fn(total)
}

// SetNotifier sets where notifications go; nil drops them.
func (s *Service) SetNotifier(n Notifier) {
	s.mu.Lock()
	s.notifier = n
	s.mu.Unlock()
}

// activate (re)starts sync after a sign-in with the new token. Signing in
// as a different user drops the previous user's cached chats first.
func (s *Service) activate(ctx context.Context, before store.Server) {
	m := s.manager()
	if m == nil {
		return
	}
	after, err := s.st.GetServer(ctx, before.ID)
	if err != nil {
		slog.Warn("cannot start sync after sign-in", "srv", before.ID, "err", err)
		return
	}
	m.Stop(before.ID)
	if before.UserID != after.UserID {
		if err := s.st.ClearCache(ctx, before.ID); err != nil {
			slog.Warn("cache clear failed", "srv", before.ID, "err", err)
		}
	}
	m.Start(after)
	s.applyFocus()
	s.co.Schedule("badge", s.refreshBadges)
}

// resume restarts sync after a sign-in whose session could not be saved: the
// worker was stopped for it, but the store still holds the previous session
// (revoked by then, so the worker shows needs_reauth).
func (s *Service) resume(ctx context.Context, before store.Server) {
	if before.SignedIn() {
		s.activate(ctx, before)
	}
}

func (s *Service) deactivate(id int64) {
	if m := s.manager(); m != nil {
		m.Stop(id)
	}
	s.co.Schedule("badge", s.refreshBadges)
}

func (s *Service) SelectServer(_ context.Context, id int64) error {
	s.mu.Lock()
	s.active = id
	s.mu.Unlock()
	s.applyFocus()
	return nil
}

func (s *Service) SetFocused(_ context.Context, focused bool) error {
	s.mu.Lock()
	s.focused = focused
	s.mu.Unlock()
	s.applyFocus()
	return nil
}

// applyFocus: only the server on screen may treat its open channel as seen.
func (s *Service) applyFocus() {
	s.focusMu.Lock()
	defer s.focusMu.Unlock()
	s.mu.Lock()
	active, focused, m := s.active, s.focused, s.mgr
	s.mu.Unlock()
	if m == nil {
		return
	}
	m.Each(func(id int64, w *mmsync.Worker) {
		f := focused && id == active
		if f && s.onFocus != nil {
			s.onFocus(id, w.State().Active())
		}
		w.SetFocused(f)
	})
}

func (s *Service) NetworkChanged(context.Context) error {
	if m := s.manager(); m != nil {
		m.NudgeAll()
	}
	return nil
}

func (s *Service) onChanged(id int64, ch state.Change) {
	if ch.Sidebar || ch.Badge {
		s.co.Schedule(fmt.Sprintf("sidebar/%d", id), func() {
			s.emit(EventSidebarChanged, map[string]any{"server_id": id})
		})
		s.co.Schedule("badge", s.refreshBadges)
	}
	for _, c := range ch.Channels {
		if c == "" {
			continue
		}
		s.co.Schedule(fmt.Sprintf("channel/%d/%s", id, c), func() {
			s.emit(EventChannelChanged, map[string]any{"server_id": id, "channel_id": c})
		})
	}
}

func (s *Service) onStatus(int64, mmsync.Status) { s.co.Schedule("badge", s.refreshBadges) }

// refreshBadges runs on the coalescer: servers_changed only when a server's
// state or badge really changed, OnBadge only when the total did.
func (s *Service) refreshBadges() {
	marks := map[int64]serverMark{}
	var total Badge
	if m := s.manager(); m != nil {
		m.Each(func(id int64, w *mmsync.Worker) {
			b := w.State().Badge()
			marks[id] = serverMark{status: w.Status(), badge: b}
			total.Unread = total.Unread || b.Unread
			total.Mentions += b.Mentions
		})
	}
	s.badgeMu.Lock()
	defer s.badgeMu.Unlock()
	s.mu.Lock()
	changed := !maps.Equal(marks, s.marks)
	totalChanged := total != s.total
	s.marks, s.total = marks, total
	fns := slices.Clone(s.badgeFns)
	s.mu.Unlock()
	if changed {
		s.emit(EventServersChanged, nil)
	}
	if totalChanged {
		for _, fn := range fns {
			fn(total)
		}
	}
}

func (s *Service) onNotify(id int64, c state.NotifyCandidate) {
	ok, why := notify.Decide(notify.Input{
		MeID: c.Me.ID, MeUsername: c.Me.Username, MeFirstName: c.Me.FirstName,
		UserNotify: c.Me.NotifyProps, MemberNotify: c.Member.NotifyProps,
		Status: c.Status.Status, DNDEndSec: c.Status.DNDEndTime, NowMs: time.Now().UnixMilli(),
		Post: c.Post, ChannelType: c.Channel.Type, Mentions: c.Mentions, Followers: c.Followers,
		CRT: c.CRT, Focused: c.Focused, ActiveChannelID: c.Active,
	})
	if !ok {
		slog.Debug("notification skipped", "srv", id, "channel", c.Post.ChannelID, "reason", why)
		return
	}
	s.nq.push(notificationFor(id, c))
}

func (s *Service) deliver(n Notification) {
	s.mu.Lock()
	nt := s.notifier
	s.mu.Unlock()
	if nt != nil {
		nt.Notify(n)
	}
}

// NotificationClicked asks the UI to open the channel of a clicked
// notification (the desktop layer also raises the window).
func (s *Service) NotificationClicked(serverID int64, channelID string) {
	s.emit(EventOpenChannel, map[string]any{"server_id": serverID, "channel_id": channelID})
}
