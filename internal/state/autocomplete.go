package state

import (
	"sort"
	"strings"
)

// Reads for the composer's autocomplete (mmsync/autocomplete.go): which
// team a channel's search runs in, which channels we are in, and the
// statuses already known.

// AutocompleteScope is the team whose members/channels/commands a
// channel's composer suggests: the channel's own, or for a DM/GM the team
// the user is on. false: not a channel we are in.
func (s *Server) AutocompleteScope(channelID string) (teamID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return "", false
	}
	if ch.Info.TeamID != "" {
		return ch.Info.TeamID, true
	}
	return s.navTeamLocked(), true
}

// Joined reports which of ids are channels we are in.
func (s *Server) Joined(ids []string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, id := range ids {
		if s.chans[id] != nil {
			out[id] = true
		}
	}
	return out
}

// Presences are the known statuses of ids (unknown ones are left out).
func (s *Server) Presences(ids []string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, id := range ids {
		if st, ok := s.presence[id]; ok && st != "" {
			out[id] = st
		}
	}
	return out
}

// Reads for search (mmsync/search.go): how a hit names its channel and the
// DMs/GMs an in: suggestion offers.

// ChannelLabel is how a search hit names its channel. Name is what in:
// takes, without the "@" of a DM/GM: a team channel's slug, a DM
// partner's username, a GM's members' usernames ("a,b,c", me too); ""
// when unknown (a DM partner's profile not loaded, a GM name the server
// cut). Display is the name the sidebar shows.
type ChannelLabel struct {
	Name    string
	Display string
	Type    string
}

// ChannelLabels labels the channels of ids we are in; others are left out.
func (s *Server) ChannelLabels(ids []string) map[string]ChannelLabel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ChannelLabel, len(ids))
	for _, id := range ids {
		if ch := s.chans[id]; ch != nil {
			out[id] = ChannelLabel{Name: s.searchNameLocked(ch), Display: s.channelNameLocked(ch), Type: ch.Info.Type}
		}
	}
	return out
}

// DirectChannel is a DM or GM we are in, for in: suggestions. Name as
// ChannelLabel.Name; PartnerID: a DM's other user (me for a self-DM).
type DirectChannel struct {
	ID        string
	Type      string
	PartnerID string
	Name      string
	Display   string
}

// DirectChannels lists the DMs and GMs we are in (not archived), by id.
func (s *Server) DirectChannels() []DirectChannel {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DirectChannel
	for _, ch := range s.chans {
		if !isDirect(ch) || ch.Info.DeleteAt != 0 {
			continue
		}
		out = append(out, DirectChannel{ID: ch.Info.ID, Type: ch.Info.Type, PartnerID: ch.Info.DMPartner(s.me.ID),
			Name: s.searchNameLocked(ch), Display: s.channelNameLocked(ch)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) searchNameLocked(ch *Chan) string {
	switch {
	case ch.Info.IsDM():
		if u, ok := s.users[ch.Info.DMPartner(s.me.ID)]; ok {
			return u.Username
		}
		return ""
	case ch.Info.IsGroup():
		return gmSearchName(ch.Info.DisplayName)
	}
	return ch.Info.Name
}

// gmDisplayNameMax is the server's ChannelNameMaxLength: a GM's display
// name (its members' usernames, sorted, ", "-joined — me too) is cut to it
// (model.GetGroupDisplayNameFromUsers(users, true)).
const gmDisplayNameMax = 64

// gmSearchName is in:'s "a,b,c" for a GM from its display name (the
// webapp's getChannelNameForSearch: all members, spaces removed); "" when
// the name may have been cut (then members may be missing and the server
// would find no such GM). The server resolves the list as a set.
func gmSearchName(display string) string {
	if display == "" || len(display) >= gmDisplayNameMax {
		return ""
	}
	var names []string
	for n := range strings.SplitSeq(display, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
