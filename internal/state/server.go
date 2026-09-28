// Package state is the in-memory hot layer of one Mattermost server:
// metadata (teams, channels, memberships, categories, preferences, users)
// and a window of the latest posts per channel. Every view the UI shows is
// built from here, so switching channels never waits on the network. The
// sync worker (internal/mmsync) is the only writer. All methods are safe for
// concurrent use; *Locked helpers expect s.mu held.
package state

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

const WindowSize = 60

type Config struct {
	CollapsedThreads        string `json:"collapsed_threads"`
	TeammateNameDisplay     string `json:"teammate_name_display"`
	LockTeammateNameDisplay bool   `json:"lock_teammate_name_display"`
	CustomEmoji             bool   `json:"custom_emoji,omitempty"`
	MaxFileSize             int64  `json:"max_file_size,omitempty"`
	EnableFileAttachments   bool   `json:"enable_file_attachments,omitempty"`
}

type Bootstrap struct {
	Me         model.User
	Status     model.Status
	Config     Config
	Prefs      []model.Preference
	Teams      []model.Team
	Channels   []model.Channel
	Members    []model.ChannelMember
	Categories map[string]model.OrderedCategories // by team id
	// CategoriesFailed: teams whose categories could not be read; they keep
	// the categories held so far instead of being blanked.
	CategoriesFailed []string
}

type Window struct {
	Posts    []model.Post `json:"posts"` // oldest → newest, ≤ WindowSize
	Loaded   bool         `json:"loaded"`
	Complete bool         `json:"complete"`  // Posts start at the channel's first post
	SyncedAt int64        `json:"synced_at"` // local ms: history complete up to here
	Stale    bool         `json:"stale"`     // posts after GapAfter may be missing
	GapAfter string       `json:"gap_after"`
}

type Chan struct {
	Info   model.Channel
	Member model.ChannelMember
	Win    Window
}

type Nav struct {
	TeamID  string            `json:"team_id"`
	Channel map[string]string `json:"channel"` // team id → last opened channel
}

type Badge struct {
	Unread   bool `json:"unread"`
	Mentions int  `json:"mentions"`
}

type prefKey struct{ cat, name string }

type Server struct {
	mu  sync.Mutex
	now func() time.Time

	me       model.User
	status   model.Status
	presence map[string]string // user id → online|away|dnd|offline|ooo; not in the snapshot
	cfg      Config
	prefs    map[prefKey]string
	teams    []model.Team
	chans    map[string]*Chan
	cats     map[string]model.OrderedCategories
	users    map[string]model.User
	emoji    map[string]string // custom emoji name → id (not in the snapshot)
	nav      Nav

	// Task 7: posts, pending, active channel.
	pending       map[string][]Pending
	released      []string // attachments of pending posts dropped since the last TakeReleased
	drafts        map[string]string
	active        string
	focused       bool
	newSince      int64
	older         []model.Post
	olderComplete bool
	suppressView  string
	guard         map[string]int64
	seen          seenSet
	gone          seenSet           // deleted replies already taken off their root's count
	winGen        uint64            // bumped by ResetWindows, see FetchMode
	orphans       []orphan          // posted events for channels not known yet
	intents       map[string]intent // our latest reaction clicks (post/emoji), see staleEchoLocked

	// Threads (see threads.go): the cache by root id, its LRU (most recent
	// last), the thread open in the panel, the epoch pages are applied with
	// (ResetThreads/MarkStale move it) and thread drafts (Task 4).
	threads      map[string]*thread
	threadLRU    []string
	openThread   string
	threadEpoch  uint64
	threadDrafts map[string]string
	// threadDraftOrder: insertion order of threadDrafts (oldest first),
	// for ThreadDraftCap eviction — see SetThreadDraft.
	threadDraftOrder []string
	// threadRedirect: the last thread opened by a reply's id → its root's
	// (RedirectThread); ThreadView resolves the former to the latter.
	threadRedirect [2]string

	liveAt int64    // Task 8: local ms of the last live WS moment
	dirty  dirtySet // Task 8
}

func New(now func() time.Time) *Server {
	if now == nil {
		now = time.Now
	}
	return &Server{
		now: now, prefs: map[prefKey]string{}, chans: map[string]*Chan{}, cats: map[string]model.OrderedCategories{},
		users: map[string]model.User{}, emoji: map[string]string{}, presence: map[string]string{}, pending: map[string][]Pending{}, drafts: map[string]string{},
		nav: Nav{Channel: map[string]string{}}, guard: map[string]int64{}, seen: newSeenSet(2000), gone: newSeenSet(2000), dirty: newDirtySet(),
		intents: map[string]intent{}, threads: map[string]*thread{}, threadDrafts: map[string]string{},
	}
}

// Bootstrap replaces all metadata with a fresh server read. Post windows of
// channels we are still in survive; channels we left disappear.
//
// crtChanged: CRT is on now and was off before, or the reverse — before
// being the previous bootstrap or a snapshot saved in the other mode (the
// admin switched CollapsedThreads, or the preference changed while we were
// away: no preferences_changed reached us). The windows then hold the
// wrong kind of posts; the caller resets them. A first bootstrap (nothing
// held) reports no change.
func (s *Server) Bootstrap(b Bootstrap) (crtChanged bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, wasCRT := s.me.ID != "", s.crtLocked()
	s.me, s.status, s.cfg = b.Me, b.Status, b.Config
	s.presence[b.Me.ID] = b.Status.Status
	s.users[b.Me.ID] = b.Me
	s.prefs = map[prefKey]string{}
	for _, p := range b.Prefs {
		s.prefs[prefKey{p.Category, p.Name}] = p.Value
	}
	s.teams = s.teams[:0]
	for _, t := range b.Teams {
		if t.DeleteAt == 0 {
			s.teams = append(s.teams, t)
		}
	}
	sort.SliceStable(s.teams, func(i, j int) bool {
		return strings.ToLower(s.teams[i].DisplayName) < strings.ToLower(s.teams[j].DisplayName)
	})
	members := make(map[string]model.ChannelMember, len(b.Members))
	for _, m := range b.Members {
		members[m.ChannelID] = m
	}
	next := make(map[string]*Chan, len(b.Channels))
	s.guard = map[string]int64{}
	for _, c := range b.Channels {
		m, ok := members[c.ID]
		if !ok {
			continue
		}
		ch := s.chans[c.ID]
		if ch == nil {
			ch = &Chan{}
		}
		ch.Info, ch.Member = c, m
		next[c.ID] = ch
		s.guard[c.ID] = c.LastPostAt
		s.dirty.chans[c.ID] = true
	}
	for id := range s.chans {
		if next[id] == nil {
			s.forgetChannelLocked(id)
		}
	}
	s.chans = next
	cats := make(map[string]model.OrderedCategories, len(b.Categories))
	for k, v := range b.Categories {
		cats[k] = v
	}
	for _, t := range b.CategoriesFailed {
		if old, ok := s.cats[t]; ok {
			cats[t] = old
		}
	}
	s.cats = cats
	if !s.hasTeamLocked(s.nav.TeamID) && len(s.teams) > 0 {
		s.nav.TeamID = s.teams[0].ID
	}
	s.dirty.meta = true
	return held && wasCRT != s.crtLocked()
}

// forgetChannelLocked drops everything local about a channel (left, kicked,
// deleted server-side). The caller removes it from s.chans.
func (s *Server) forgetChannelLocked(id string) {
	delete(s.drafts, id)
	for _, p := range s.pending[id] {
		s.releaseLocked(p)
	}
	delete(s.pending, id)
	s.forgetThreadsLocked(id)
	s.dirty.dropChan(id)
}

func (s *Server) hasTeamLocked(id string) bool {
	for _, t := range s.teams {
		if t.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) Me() model.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.me
}

func (s *Server) SetStatus(st model.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
}

func (s *Server) CRT() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crtLocked()
}

// MaxFileSize is the server's upload limit in bytes (0: unknown — no
// bootstrap yet, or the server did not report it).
func (s *Server) MaxFileSize() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.MaxFileSize
}

func (s *Server) FileAttachmentsEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.EnableFileAttachments
}

func (s *Server) TeamIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.teams))
	for i, t := range s.teams {
		out[i] = t.ID
	}
	return out
}

func (s *Server) prefLocked(cat, name, def string) string {
	if v, ok := s.prefs[prefKey{cat, name}]; ok {
		return v
	}
	return def
}

// crtLocked mirrors the webapp's isCollapsedThreadsEnabled.
func (s *Server) crtLocked() bool {
	switch s.cfg.CollapsedThreads {
	case "", "disabled":
		return false
	case "always_on":
		return true
	}
	def := "off"
	if s.cfg.CollapsedThreads == "default_on" {
		def = "on"
	}
	return s.prefLocked("display_settings", "collapsed_reply_threads", def) == "on"
}

func (s *Server) nameFormatLocked() string {
	if s.cfg.LockTeammateNameDisplay && s.cfg.TeammateNameDisplay != "" {
		return s.cfg.TeammateNameDisplay
	}
	if v := s.prefLocked("display_settings", "name_format", ""); v != "" {
		return v
	}
	if s.cfg.TeammateNameDisplay != "" {
		return s.cfg.TeammateNameDisplay
	}
	return "username"
}

// displayNameLocked: "" for users not loaded yet (the UI shows a placeholder).
func (s *Server) displayNameLocked(userID string) string {
	u, ok := s.users[userID]
	if !ok {
		return ""
	}
	switch s.nameFormatLocked() {
	case "full_name":
		if n := u.FullName(); n != "" {
			return n
		}
	case "nickname_full_name":
		if u.Nickname != "" {
			return u.Nickname
		}
		if n := u.FullName(); n != "" {
			return n
		}
	}
	return u.Username
}

func (s *Server) channelNameLocked(c *Chan) string {
	if c.Info.IsDM() {
		if n := s.displayNameLocked(c.Info.DMPartner(s.me.ID)); n != "" {
			return n
		}
		return c.Info.DMPartner(s.me.ID)
	}
	if c.Info.DisplayName != "" {
		return c.Info.DisplayName
	}
	return c.Info.Name
}

// unreadLocked mirrors the webapp's calculateUnreadCount.
func (s *Server) unreadLocked(c *Chan) (unread bool, mentions int) {
	var msgs, ment int64
	if s.crtLocked() {
		msgs, ment = c.Info.TotalMsgCountRoot-c.Member.MsgCountRoot, c.Member.MentionCountRoot
	} else {
		msgs, ment = c.Info.TotalMsgCount-c.Member.MsgCount, c.Member.MentionCount
	}
	return ment > 0 || (!c.Member.Muted() && msgs > 0), int(ment)
}

// excludedFromSumsLocked reports whether c must be skipped when summing
// unread/mentions for a badge or a team (the webapp's getUnreadStatus):
// muted channels, archived channels, and DMs whose partner is known to be
// deactivated. A DM partner not loaded yet still counts.
func (s *Server) excludedFromSumsLocked(c *Chan) bool {
	if c.Info.DeleteAt != 0 || c.Member.Muted() {
		return true
	}
	if c.Info.IsDM() {
		if u, ok := s.users[c.Info.DMPartner(s.me.ID)]; ok && u.DeleteAt != 0 {
			return true
		}
	}
	return false
}

// Badge sums mentions across the server, skipping muted and archived
// channels and DMs with a deactivated partner (the webapp's getUnreadStatus).
func (s *Server) Badge() Badge {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b Badge
	for _, c := range s.chans {
		if s.excludedFromSumsLocked(c) {
			continue
		}
		u, m := s.unreadLocked(c)
		b.Unread = b.Unread || u
		b.Mentions += m
	}
	return b
}

func (s *Server) SetUsers(us []model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range us {
		s.users[u.ID] = u
		s.dirty.users[u.ID] = true
	}
}

// KnownUserIDs lists the users held (restored from the snapshot or loaded).
func (s *Server) KnownUserIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.users))
	for id := range s.users {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// RefreshUsers stores a fresh read of users we already hold; a profile a
// newer event already brought (higher update_at) is kept, users we do not
// hold are ignored. Reports whether anything the UI shows changed (names,
// picture version, deactivation).
func (s *Server) RefreshUsers(us []model.User) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, u := range us {
		old, ok := s.users[u.ID]
		if !ok || old.UpdateAt > u.UpdateAt {
			continue
		}
		if u.ID == s.me.ID && u.NotifyProps == nil {
			u.NotifyProps = old.NotifyProps
		}
		changed = changed || old.Username != u.Username || old.FirstName != u.FirstName || old.LastName != u.LastName ||
			old.Nickname != u.Nickname || old.DeleteAt != u.DeleteAt || old.IsBot != u.IsBot ||
			old.LastPictureUpdate != u.LastPictureUpdate
		s.users[u.ID] = u
		s.dirty.users[u.ID] = true
	}
	return changed
}

// MissingUserIDs lists users the views need (DM partners, post authors)
// that are not loaded yet.
func (s *Server) MissingUserIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	need := map[string]bool{}
	for _, c := range s.chans {
		if p := c.Info.DMPartner(s.me.ID); p != "" {
			need[p] = true
		}
		for _, p := range c.Win.Posts {
			need[p.UserID] = true
		}
	}
	for _, p := range s.older {
		need[p.UserID] = true
	}
	for _, t := range s.threads {
		need[t.root.UserID] = true
		for _, p := range t.replies {
			need[p.UserID] = true
		}
	}
	var out []string
	for id := range need {
		if _, ok := s.users[id]; !ok && id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func atoiDefault(s string, def int64) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}
