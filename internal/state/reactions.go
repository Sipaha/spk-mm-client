package state

import (
	"encoding/json"
	"slices"
	"sort"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

const (
	// intentTTL bounds how long our latest click on a post+emoji decides
	// which of our own reaction events are stale echoes.
	intentTTL      = 30 * time.Second
	maxIntents     = 200
	maxRecentEmoji = 27 // the webapp's MAXIMUM_RECENT_EMOJI
)

type intent struct {
	add   bool
	until time.Time
	// pinned: the click's request is waiting for a retry — the intent does
	// not expire until it is sent (PinReactIntent).
	pinned bool
}

func intentKey(postID, emoji string) string { return postID + "/" + emoji }

// channelOfPostLocked finds the channel of a post held in memory.
func (s *Server) channelOfPostLocked(postID string) (string, bool) {
	for id, ch := range s.chans {
		if indexOf(ch.Win.Posts, postID) >= 0 {
			return id, true
		}
	}
	if s.active != "" && indexOf(s.older, postID) >= 0 {
		return s.active, true
	}
	if _, t, ok := s.threadPostLocked(postID); ok {
		return t.channelID, true
	}
	return "", false
}

// ReactLocalWas applies our own reaction at once — the click lands before
// the server answers — and records it as the intent for the post+emoji; it
// reports whether our reaction was there before the click, and ok=false if
// the post is not in memory.
func (s *Server) ReactLocalWas(postID, emoji string, add bool) (ch Change, was, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}, false, false
	}
	now := s.now()
	if len(s.intents) >= maxIntents {
		for k, in := range s.intents {
			if !in.pinned && now.After(in.until) {
				delete(s.intents, k)
			}
		}
	}
	k := intentKey(postID, emoji)
	// A pinned intent's request waits for a retry, which will send this
	// click: it stays pinned.
	s.intents[k] = intent{add: add, until: now.Add(intentTTL), pinned: s.intents[k].pinned}
	// "Was mine" is read from the copies before the click. When they
	// disagree (feed vs thread) the truth is unknown: was = !add, so the
	// request is always sent — both endpoints are idempotent.
	some, all := s.mineInCopiesLocked(postID, emoji)
	switch {
	case some != all:
		was = !add
	default:
		was = all
	}
	s.reactLocked(id, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji, CreateAt: now.UnixMilli()}, add, false)
	return Change{Channels: []string{id}, Threads: s.threadsHoldingLocked(postID)}, was, true
}

// mineInCopiesLocked reports whether some / all held copies of postID
// (windows, the open channel's history, the thread cache) carry our
// reaction emoji.
func (s *Server) mineInCopiesLocked(postID, emoji string) (some, all bool) {
	n := 0
	all = true
	see := func(p model.Post) {
		n++
		mine := p.Metadata != nil && slices.ContainsFunc(p.Metadata.Reactions, func(x model.Reaction) bool {
			return x.UserID == s.me.ID && x.EmojiName == emoji
		})
		some, all = some || mine, all && mine
	}
	for _, ch := range s.chans {
		if i := indexOf(ch.Win.Posts, postID); i >= 0 {
			see(ch.Win.Posts[i])
		}
	}
	if i := indexOf(s.older, postID); i >= 0 {
		see(s.older[i])
	}
	for _, t := range s.threads {
		if t.root.ID == postID {
			see(t.root)
		}
		if i := indexOf(t.replies, postID); i >= 0 {
			see(t.replies[i])
		}
	}
	return some, all && n > 0
}

// SetMyReaction sets our reaction on a post to a state known from the
// server (a rollback) and ends the intent for the pair: the
// post shows exactly mine, whatever clicks were applied before.
func (s *Server) SetMyReaction(postID, emoji string, mine bool) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.intents, intentKey(postID, emoji))
	ch, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}
	}
	s.reactLocked(ch, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji, CreateAt: s.now().UnixMilli()}, mine, false)
	return Change{Channels: []string{ch}, Threads: s.threadsHoldingLocked(postID)}
}

// ForgetReactIntent drops the intent of a click that was never sent (a
// later click cancelled it before the request went out): no echo will end it.
func (s *Server) ForgetReactIntent(postID, emoji string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.intents, intentKey(postID, emoji))
}

// PinReactIntent keeps the intent of the pair from expiring while its
// request waits for a retry (pin), or lets it expire intentTTL from now once
// the request went through (unpin). A missing intent — its echo already
// came — stays missing.
func (s *Server) PinReactIntent(postID, emoji string, pin bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := intentKey(postID, emoji)
	if in, ok := s.intents[k]; ok {
		in.pinned, in.until = pin, s.now().Add(intentTTL)
		s.intents[k] = in
	}
}

// staleEchoLocked: an event about our own reaction that contradicts our
// latest click is the late echo of an earlier click — drop it. The echo
// that matches the click ends the intent; an expired intent decides nothing.
func (s *Server) staleEchoLocked(r model.Reaction, add bool) bool {
	if r.UserID != s.me.ID {
		return false
	}
	k := intentKey(r.PostID, r.EmojiName)
	in, ok := s.intents[k]
	if !ok {
		return false
	}
	if (!in.pinned && s.now().After(in.until)) || in.add == add {
		delete(s.intents, k)
		return false
	}
	return true
}

type recentEmoji struct {
	Name       string `json:"name"`
	UsageCount int    `json:"usageCount"`
}

func (s *Server) recentLocked() []recentEmoji {
	var list []recentEmoji
	if v := s.prefs[prefKey{"recent_emojis", s.me.ID}]; v != "" {
		if json.Unmarshal([]byte(v), &list) != nil {
			return nil
		}
	}
	return list
}

// RecentEmojis lists recently used emoji, most used first (the webapp keeps
// them sorted ascending and shows the list from its end).
func (s *Server) RecentEmojis() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.recentLocked()
	out := make([]string, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Name != "" {
			out = append(out, list[i].Name)
		}
	}
	return out
}

// BumpRecentEmoji records a use like the webapp's addRecentEmojis: the name
// moves to the end with usageCount+1, only the last 27 stay, the list is
// sorted by usageCount ascending. Applies at once, in memory — the caller
// (Worker.React) calls this the moment an add is issued, before any
// network round trip, so RecentEmojis/EmojiInfo reflect it immediately;
// see RecentPreference for saving it to the server afterwards.
func (s *Server) BumpRecentEmoji(name string) model.Preference {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.recentLocked()
	count := 1
	if i := slices.IndexFunc(list, func(e recentEmoji) bool { return e.Name == name }); i >= 0 {
		count = list[i].UsageCount + 1
		list = slices.Delete(list, i, i+1)
	}
	list = append(list, recentEmoji{Name: name, UsageCount: count})
	if len(list) > maxRecentEmoji {
		list = list[len(list)-maxRecentEmoji:]
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].UsageCount < list[j].UsageCount })
	return s.setRecentLocked(list)
}

// RecentPreference returns the recent_emojis preference reflecting the
// *current* in-memory list, without changing anything — Worker.React bumps
// the list at issue time (BumpRecentEmoji) and, once the reaction it
// belongs to is confirmed by the server, saves whatever this returns at
// that later point: always the live value, never a snapshot from issue
// time, so it can't clobber a different emoji's concurrent bump with stale
// data.
func (s *Server) RecentPreference() model.Preference {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.recentLocked())
	return model.Preference{UserID: s.me.ID, Category: "recent_emojis", Name: s.me.ID, Value: string(b)}
}

// setRecentLocked serializes list as the recent_emojis preference, records
// it in prefs (marking metadata dirty) and returns it to save.
func (s *Server) setRecentLocked(list []recentEmoji) model.Preference {
	b, _ := json.Marshal(list)
	p := model.Preference{UserID: s.me.ID, Category: "recent_emojis", Name: s.me.ID, Value: string(b)}
	s.prefs[prefKey{p.Category, p.Name}] = p.Value
	s.dirty.meta = true
	return p
}

// ---- who reacted (reaction chip tooltip/modal) ----

// Reactor is one entry in a "who reacted" list: a user who reacted,
// resolved to a display name and avatar version where the profile is
// known. Name == "" means the profile is not loaded (an unknown/unfetched
// reactor — the UI shows a placeholder).
type Reactor struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

// ReactionUsersView answers "who reacted with this emoji": everyone who
// reacted except me (the UI adds "You"), oldest reaction first. Unknown
// counts the Users whose Name is still "".
type ReactionUsersView struct {
	Users   []Reactor `json:"users"`
	Unknown int       `json:"unknown"`
}

// ReactorIDs lists the ids of everyone (except me) who put emoji on
// postID, oldest reaction first — read from whichever copy of the post
// state currently holds (feed window, history or the thread cache; see
// findPostLocked). ok=false: the post is not held anywhere right now (the
// caller has nothing to show). A held post with no matching reaction
// returns an empty, ok=true slice.
func (s *Server) ReactorIDs(postID, emoji string) (ids []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, found := s.findPostLocked(postID)
	if !found {
		return nil, false
	}
	if p.Metadata == nil {
		return []string{}, true
	}
	list := make([]model.Reaction, 0, len(p.Metadata.Reactions))
	for _, r := range p.Metadata.Reactions {
		if r.EmojiName == emoji && r.UserID != s.me.ID {
			list = append(list, r)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreateAt < list[j].CreateAt })
	ids = make([]string, len(list))
	for i, r := range list {
		ids[i] = r.UserID
	}
	return ids, true
}

// MissingAmong filters ids to the ones without a loaded profile — the set
// a caller needs to fetch (e.g. POST users/ids) before ResolveReactors can
// name everyone.
func (s *Server) MissingAmong(ids []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, id := range ids {
		if _, ok := s.users[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// ResolveReactors resolves ids (in the given order) to display
// names/avatar versions from whatever profiles state already holds — call
// it after fetching MissingAmong's ids so as many resolve as possible; a
// profile that never loaded (e.g. the fetch failed) stays an empty Reactor
// name and counts toward Unknown, never blocking the caller.
func (s *Server) ResolveReactors(ids []string) ReactionUsersView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ReactionUsersView{Users: make([]Reactor, len(ids))}
	for i, id := range ids {
		name := s.displayNameLocked(id)
		if name == "" {
			out.Unknown++
		}
		out.Users[i] = Reactor{ID: id, Name: name, Avatar: s.avatarLocked(id)}
	}
	return out
}
