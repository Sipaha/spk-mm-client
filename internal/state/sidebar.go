package state

import (
	"sort"
	"strings"
	"unicode"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type TeamItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Unread      bool   `json:"unread"`
	Mentions    int    `json:"mentions"`
}

type ChannelItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Unread   bool   `json:"unread"`
	Mentions int    `json:"mentions"`
	Muted    bool   `json:"muted"`
	// DMs only: the partner, their picture version and presence.
	UserID string `json:"user_id,omitempty"`
	Avatar string `json:"avatar,omitempty"`
	Status string `json:"status,omitempty"`
	Bot    bool   `json:"bot,omitempty"`
}

type CategoryView struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	Name      string        `json:"name"`
	Collapsed bool          `json:"collapsed"`
	Channels  []ChannelItem `json:"channels"`
}

type SidebarView struct {
	TeamID            string         `json:"team_id"`
	SelectedChannelID string         `json:"selected_channel_id"`
	Teams             []TeamItem     `json:"teams"`
	Categories        []CategoryView `json:"categories"`
}

const defaultDMLimit = 40

// Sidebar builds the sidebar of teamID ("" = the last used team) and makes
// it the current team.
func (s *Server) Sidebar(teamID string) SidebarView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasTeamLocked(teamID) {
		teamID = s.nav.TeamID
	}
	if teamID != s.nav.TeamID {
		s.nav.TeamID = teamID
		s.dirty.meta = true
	}
	v := SidebarView{TeamID: teamID, Teams: s.teamItemsLocked(), Categories: s.categoriesLocked(teamID)}
	v.SelectedChannelID = s.selectedLocked(teamID, v.Categories)
	return v
}

func (s *Server) teamItemsLocked() []TeamItem {
	out := make([]TeamItem, 0, len(s.teams))
	for _, t := range s.teams {
		it := TeamItem{ID: t.ID, Name: t.Name, DisplayName: t.DisplayName}
		for _, c := range s.chans {
			if c.Info.TeamID != t.ID || s.excludedFromSumsLocked(c) {
				continue
			}
			u, m := s.unreadLocked(c)
			it.Unread = it.Unread || u
			it.Mentions += m
		}
		// Its followed threads' mentions (CRT; threadcounts.go) — on the
		// team, never on a channel row.
		if n := s.threadMentionsLocked(t.ID); n > 0 {
			it.Unread, it.Mentions = true, it.Mentions+n
		}
		out = append(out, it)
	}
	return out
}

func (s *Server) inTeamLocked(c *Chan, teamID string) bool {
	return c.Info.DeleteAt == 0 && (c.Info.TeamID == teamID || c.Info.TeamID == "")
}

func isDirect(c *Chan) bool { return c.Info.IsDM() || c.Info.IsGroup() }

func (s *Server) categoriesLocked(teamID string) []CategoryView {
	oc, ok := s.cats[teamID]
	if !ok || len(oc.Categories) == 0 {
		oc = model.OrderedCategories{
			Categories: []model.SidebarCategory{
				{ID: "channels", Type: "channels", DisplayName: "Channels", Sorting: "alpha"},
				{ID: "direct_messages", Type: "direct_messages", DisplayName: "Direct Messages", Sorting: "recent"},
			},
			Order: []string{"channels", "direct_messages"},
		}
	}
	byID := map[string]model.SidebarCategory{}
	for _, c := range oc.Categories {
		byID[c.ID] = c
	}
	order := oc.Order
	if len(order) == 0 {
		for _, c := range oc.Categories {
			order = append(order, c.ID)
		}
	}
	placed := map[string]bool{}
	var cats []model.SidebarCategory
	for _, id := range order {
		c, ok := byID[id]
		if !ok {
			continue
		}
		var keep []string
		for _, chID := range c.ChannelIDs {
			if ch := s.chans[chID]; ch != nil && s.inTeamLocked(ch, teamID) && !placed[chID] {
				keep = append(keep, chID)
				placed[chID] = true
			}
		}
		c.ChannelIDs = keep
		cats = append(cats, c)
	}
	// Channels the categories don't know yet (joined a moment ago) go to
	// the default category of their kind.
	var extraCh, extraDM []string
	for id, ch := range s.chans {
		if placed[id] || !s.inTeamLocked(ch, teamID) {
			continue
		}
		if isDirect(ch) {
			extraDM = append(extraDM, id)
		} else {
			extraCh = append(extraCh, id)
		}
	}
	sort.Strings(extraCh)
	sort.Strings(extraDM)
	for i := range cats {
		switch cats[i].Type {
		case "channels":
			cats[i].ChannelIDs = append(cats[i].ChannelIDs, extraCh...)
			extraCh = nil
		case "direct_messages":
			cats[i].ChannelIDs = append(cats[i].ChannelIDs, extraDM...)
			extraDM = nil
		}
	}
	out := make([]CategoryView, 0, len(cats))
	for _, c := range cats {
		ids := c.ChannelIDs
		if c.Type == "direct_messages" {
			ids = s.visibleDMsLocked(ids)
		} else {
			var vis []string
			for _, id := range ids {
				ch := s.chans[id]
				u, _ := s.unreadLocked(ch)
				if !isDirect(ch) || s.dmShownLocked(ch, u) {
					vis = append(vis, id)
				}
			}
			ids = vis
		}
		ids = s.sortLocked(ids, c.Sorting)
		cv := CategoryView{ID: c.ID, Type: c.Type, Name: c.DisplayName, Collapsed: c.Collapsed, Channels: []ChannelItem{}}
		for _, id := range ids {
			ch := s.chans[id]
			u, m := s.unreadLocked(ch)
			it := ChannelItem{ID: id, Name: s.channelNameLocked(ch), Type: ch.Info.Type,
				Unread: u, Mentions: m, Muted: ch.Member.Muted()}
			if ch.Info.IsDM() {
				partner := ch.Info.DMPartner(s.me.ID)
				it.UserID, it.Avatar = partner, s.avatarLocked(partner)
				if pu, ok := s.users[partner]; ok && pu.IsBot {
					it.Bot = true
				} else {
					it.Status = s.presenceLocked(partner)
				}
			}
			cv.Channels = append(cv.Channels, it)
		}
		out = append(out, cv)
	}
	return out
}

// dmShownLocked: the webapp's manual-close filter.
func (s *Server) dmShownLocked(c *Chan, unread bool) bool {
	if unread || c.Info.ID == s.active {
		return true
	}
	var v string
	var ok bool
	if c.Info.IsDM() {
		v, ok = s.prefs[prefKey{"direct_channel_show", c.Info.DMPartner(s.me.ID)}]
	} else {
		v, ok = s.prefs[prefKey{"group_channel_show", c.Info.ID}]
	}
	return ok && v != "false"
}

func (s *Server) dmLastSeenLocked(c *Chan) int64 {
	t := c.Member.LastViewedAt
	t = max(t, atoiDefault(s.prefLocked("channel_approximate_view_time", c.Info.ID, ""), 0))
	t = max(t, atoiDefault(s.prefLocked("channel_open_time", c.Info.ID, ""), 0))
	return t
}

// visibleDMsLocked: manual-close filter, then the autoclose limit
// (sidebar_settings/limit_visible_dms_gms, default 40) keeping the active,
// unread and most recently seen conversations.
func (s *Server) visibleDMsLocked(ids []string) []string {
	type cand struct {
		id     string
		active bool
		unread bool
		seen   int64
	}
	var cs []cand
	unreadN := 0
	for _, id := range ids {
		ch := s.chans[id]
		u, _ := s.unreadLocked(ch)
		if !s.dmShownLocked(ch, u) {
			continue
		}
		if u {
			unreadN++
		}
		cs = append(cs, cand{id, id == s.active, u, s.dmLastSeenLocked(ch)})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.active != b.active {
			return a.active
		}
		if a.unread != b.unread {
			return a.unread
		}
		return a.seen > b.seen
	})
	limit := int(atoiDefault(s.prefLocked("sidebar_settings", "limit_visible_dms_gms", ""), defaultDMLimit))
	limit = max(limit, unreadN)
	if limit > 0 && len(cs) > limit {
		cs = cs[:limit]
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.id
	}
	return out
}

func (s *Server) sortLocked(ids []string, sorting string) []string {
	out := append([]string(nil), ids...)
	switch sorting {
	case "manual":
		return out
	case "recent":
		crt := s.crtLocked()
		key := func(id string) int64 {
			c := s.chans[id].Info
			last := c.LastPostAt
			if crt && c.LastRootPostAt != 0 {
				last = c.LastRootPostAt
			}
			return max(last, c.CreateAt)
		}
		sort.SliceStable(out, func(i, j int) bool { return key(out[i]) > key(out[j]) })
	default: // "", "alpha"
		sort.SliceStable(out, func(i, j int) bool {
			a, b := s.chans[out[i]], s.chans[out[j]]
			if a.Member.Muted() != b.Member.Muted() {
				return b.Member.Muted()
			}
			return naturalLess(s.channelNameLocked(a), s.channelNameLocked(b))
		})
	}
	return out
}

// naturalLess compares case-insensitively with digit runs as numbers
// ("ch2" < "ch10"), like localeCompare(…, {numeric: true}).
func naturalLess(a, b string) bool {
	ar, br := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if unicode.IsDigit(ar[i]) && unicode.IsDigit(br[j]) {
			si := i
			for i < len(ar) && unicode.IsDigit(ar[i]) {
				i++
			}
			sj := j
			for j < len(br) && unicode.IsDigit(br[j]) {
				j++
			}
			na := strings.TrimLeft(string(ar[si:i]), "0")
			nb := strings.TrimLeft(string(br[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ar[i] != br[j] {
			return ar[i] < br[j]
		}
		i++
		j++
	}
	return len(ar)-i < len(br)-j
}

func (s *Server) selectedLocked(teamID string, cats []CategoryView) string {
	if id := s.nav.Channel[teamID]; id != "" {
		if c := s.chans[id]; c != nil && s.inTeamLocked(c, teamID) {
			return id
		}
	}
	for _, c := range s.chans {
		if c.Info.TeamID == teamID && c.Info.Name == "town-square" && c.Info.DeleteAt == 0 {
			return c.Info.ID
		}
	}
	for _, cat := range cats {
		if len(cat.Channels) > 0 {
			return cat.Channels[0].ID
		}
	}
	return ""
}
