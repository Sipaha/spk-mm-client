package state

import (
	"maps"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

// Mentions in followed threads (CRT only). A reply's mention does not
// count on its channel under CRT (the *_root counters): the server keeps it
// on the thread membership. The worker reads the totals with the metadata
// (GET users/me/teams/unread?include_collapsed_threads — per team, without
// DM/GM threads — and the DM/GM part as threads?totalsOnly minus the same
// with excludeDirect), then events move them: thread_updated and
// thread_read_changed carry unread_mentions and previous_unread_mentions.
//
// They raise the server's and the team's badge, never a channel row (the
// totals are per team; ruling of the threads plan). Unread threads without
// a mention raise nothing — there is no Threads view to clear them in.
// Bounded: one number per team plus "" for the DM/GMs; not in the snapshot.
//
// The totals are a read, the events a stream: an event that arrives while
// they are read may or may not be in them. Bootstrap sets threadGuard
// (cleared with the post guard, ClearGuard); an event with a mention
// change under it is not counted but asks for a fresh read
// (Effects.RereadThreadCounts), which replaces the totals whole — and only
// if no such event moved them while it was in flight (ThreadCountsToken).

// ThreadCountsToken is what a reread of the totals started from; see
// SetThreadMentions.
type ThreadCountsToken struct{ at, n uint64 }

// bootstrapThreadCountsLocked installs the totals of a metadata read.
func (s *Server) bootstrapThreadCountsLocked(b Bootstrap) {
	s.threadCountsAt++
	crt := s.crtLocked()
	switch {
	case !crt:
		s.threadMentions = nil
	case b.ThreadMentions != nil:
		s.threadMentions = maps.Clone(b.ThreadMentions)
	case b.ThreadMentionsFailed: // the ones held so far stay
	default:
		s.threadMentions = nil
	}
	s.threadGuard = crt
	s.threadCountsDirty = crt && b.ThreadMentionsFailed && b.ThreadMentions == nil
}

// threadMentionsLocked: the unread mentions in team's followed threads
// ("": the DM/GMs'); 0 without CRT.
func (s *Server) threadMentionsLocked(team string) int {
	if !s.crtLocked() {
		return 0
	}
	return int(max(0, s.threadMentions[team]))
}

// crtSwitchedLocked: a preferences_changed turned CRT on or off. Off: no
// thread mention is left; on: the totals are read afresh.
func (s *Server) crtSwitchedLocked(eff *Effects) {
	s.threadCountsAt++
	s.threadMentions, s.threadGuard, s.threadCountsDirty = nil, false, false
	if s.crtLocked() {
		eff.RereadThreadCounts = true
	}
}

// threadMentionDeltaLocked moves the thread mentions of channelID's team
// ("" for a DM/GM — the channel decides: thread_read_changed's broadcast
// team is the one the read was sent to) by delta. A thread of a channel we
// are not in is not in the server's totals either.
func (s *Server) threadMentionDeltaLocked(channelID string, delta int64, eff *Effects) {
	ch := s.chans[channelID]
	if delta == 0 || ch == nil || !s.crtLocked() {
		return
	}
	s.threadCountsN++
	if s.threadGuard {
		eff.RereadThreadCounts = true
		return
	}
	key := ch.Info.TeamID
	if isDirect(ch) {
		key = ""
	}
	if s.threadMentions == nil {
		s.threadMentions = map[string]int64{}
	}
	s.threadMentions[key] = max(0, s.threadMentions[key]+delta)
	eff.Sidebar, eff.Badge = true, true
}

// onThreadUpdatedLocked: a reply in (or a change of) a thread we follow.
// Its reply count reaches the root held in the feed and the cache; the
// open thread is read again if it says something is unread; the mention
// delta goes to the totals.
func (s *Server) onThreadUpdatedLocked(ev ws.Event, eff *Effects) {
	d, err := ws.DecodeThreadUpdated(ev)
	if err != nil || d.Thread.PostID == "" {
		return
	}
	th := d.Thread
	channelID, count, last := "", th.ReplyCount, th.LastReplyAt
	if th.Post != nil {
		channelID = th.Post.ChannelID
		if last == 0 {
			count, last = th.Post.ReplyCount, th.Post.LastReplyAt
		}
	}
	if ch := s.chans[channelID]; ch != nil && s.eachRootLocked(ch, th.PostID, func(r *model.Post) bool {
		// Only newer: posted already applied its own reply's total, and a
		// deletion may have lowered it since (Task 2 rules).
		if last <= r.LastReplyAt {
			return false
		}
		r.ReplyCount, r.LastReplyAt = count, last
		return true
	}) {
		eff.Merge(Change{Channels: []string{channelID}, Threads: s.threadsOfLocked(model.Post{ID: th.PostID})})
	}
	if th.PostID == s.openThread {
		s.openRead.noFollow = false // the server says we follow it
		if s.focused && s.crtLocked() {
			eff.ReadThread = th.PostID
		}
	}
	if !d.HasPrevious {
		// No previous value (MarkChannelAsUnreadFromPost sends the thread
		// alone): no delta to take — read the totals again.
		if s.chans[channelID] != nil {
			s.threadCountsChangedLocked(eff)
		}
		return
	}
	s.threadMentionDeltaLocked(channelID, th.UnreadMentions-d.PreviousUnreadMentions, eff)
}

// threadCountsChangedLocked: the totals changed by an amount an event does
// not tell (every thread read, a follow change, a thread_updated without
// previous values): a reread in flight is unsettled, another is asked for.
func (s *Server) threadCountsChangedLocked(eff *Effects) {
	if !s.crtLocked() {
		return
	}
	s.threadCountsN++
	eff.RereadThreadCounts = true
}

// onThreadFollowChangedLocked: we followed or unfollowed a thread (here or
// elsewhere — for an unfollow this is the only event, app/user.go
// UpdateThreadFollowForUser): the totals cover followed threads only.
func (s *Server) onThreadFollowChangedLocked(eff *Effects) {
	s.threadCountsChangedLocked(eff)
}

// onThreadReadChangedLocked: a thread was read (here or elsewhere). Without
// a thread id every thread of a team or channel was: no delta to apply, the
// totals are read again.
func (s *Server) onThreadReadChangedLocked(ev ws.Event, eff *Effects) {
	d, err := ws.DecodeThreadReadChanged(ev)
	if err != nil || !s.crtLocked() {
		return
	}
	if d.ThreadID == "" {
		s.threadCountsChangedLocked(eff)
		return
	}
	s.threadMentionDeltaLocked(d.ChannelID, d.UnreadMentions-d.PreviousUnreadMentions, eff)
}

// ThreadCountsFetch is what a reread of the totals needs: whether CRT is
// on, the team to read the DM/GM part through (the current one; DM/GM
// threads are in every team's totals) and the token to install it with.
func (s *Server) ThreadCountsFetch() (crt bool, team string, tok ThreadCountsToken) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crtLocked(), s.navTeamLocked(), ThreadCountsToken{s.threadCountsAt, s.threadCountsN}
}

// SetThreadMentions installs reread totals whole. Not installed: CRT went
// off or a metadata read installed fresher ones since tok (nothing to do),
// or — retry — a thread event moved the counts while the read was in
// flight (it may or may not be in it); force installs anyway (the last try).
func (s *Server) SetThreadMentions(m map[string]int64, tok ThreadCountsToken, force bool) (installed, retry bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.crtLocked() || tok.at != s.threadCountsAt {
		return false, false
	}
	if tok.n != s.threadCountsN && !force {
		return false, true
	}
	s.threadMentions = maps.Clone(m)
	if s.threadMentions == nil {
		s.threadMentions = map[string]int64{}
	}
	s.threadCountsDirty = false
	return true, false
}

// MarkThreadCountsDirty records that a reread of the totals failed: what it
// was to replace (an event under the guard, "all read"…) is not in the
// totals held. ThreadCountsDirty asks the worker to read them again when
// it is live again; installing totals clears it.
func (s *Server) MarkThreadCountsDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadCountsDirty = s.crtLocked()
}

func (s *Server) ThreadCountsDirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadCountsDirty && s.crtLocked()
}

// NavTeam is the team the user is on (the sidebar's), "" before any.
func (s *Server) NavTeam() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.navTeamLocked()
}

func (s *Server) navTeamLocked() string {
	if s.hasTeamLocked(s.nav.TeamID) {
		return s.nav.TeamID
	}
	if len(s.teams) > 0 {
		return s.teams[0].ID
	}
	return ""
}
