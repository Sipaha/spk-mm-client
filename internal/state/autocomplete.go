package state

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
