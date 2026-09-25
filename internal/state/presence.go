package state

import (
	"sort"
	"strconv"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// SetPresence records statuses from a status poll and reports the views to
// refresh; my own also updates the status the notification rules read.
func (s *Server) SetPresence(list []model.Status) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, st := range list {
		if st.UserID == "" {
			continue
		}
		if s.presence[st.UserID] != st.Status {
			s.presence[st.UserID] = st.Status
			changed = true
		}
		if st.UserID == s.me.ID {
			s.status.Status, s.status.DNDEndTime = st.Status, st.DNDEndTime
		}
	}
	if !changed {
		return Change{}
	}
	return s.presenceChangeLocked()
}

// presenceChangeLocked: statuses show in the sidebar (DMs) and the feed.
func (s *Server) presenceChangeLocked() Change {
	c := Change{Sidebar: true}
	if s.active != "" {
		c.Channels = []string{s.active}
	}
	return c
}

// StatusTargets lists the users whose presence is on screen — me first,
// then the partners of DMs the sidebar shows and the authors of the open
// channel (bots have none), at most limit. Mattermost sends status_change
// only to the user it is about, so these are polled (the webapp does the same).
func (s *Server) StatusTargets(limit int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]bool{}
	add := func(id string) {
		if id == "" || id == s.me.ID {
			return
		}
		if u, ok := s.users[id]; ok && u.IsBot {
			return
		}
		set[id] = true
	}
	for _, c := range s.chans {
		if !c.Info.IsDM() || c.Info.DeleteAt != 0 {
			continue
		}
		if unread, _ := s.unreadLocked(c); s.dmShownLocked(c, unread) {
			add(c.Info.DMPartner(s.me.ID))
		}
	}
	if ch := s.chans[s.active]; ch != nil {
		for _, p := range ch.Win.Posts {
			if !p.IsSystem() {
				add(p.UserID)
			}
		}
		for _, p := range s.older {
			if !p.IsSystem() {
				add(p.UserID)
			}
		}
	}
	out := make([]string, 0, len(set)+1)
	if s.me.ID != "" {
		out = append(out, s.me.ID)
	}
	others := make([]string, 0, len(set))
	for id := range set {
		others = append(others, id)
	}
	sort.Strings(others)
	out = append(out, others...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Server) presenceLocked(userID string) string {
	if u, ok := s.users[userID]; ok && u.IsBot {
		return ""
	}
	return s.presence[userID]
}

// avatarLocked is the picture version put into avatar URLs; "" while the
// profile is not loaded (the UI shows initials and fetches nothing).
func (s *Server) avatarLocked(userID string) string {
	u, ok := s.users[userID]
	if !ok {
		return ""
	}
	return strconv.FormatInt(u.LastPictureUpdate, 10)
}
