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
	changed := s.reactLocked(id, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji, CreateAt: now.UnixMilli()}, add)
	// The post is in memory, so an add that changed nothing found ours
	// there and a remove that changed something removed it.
	return Change{Channels: []string{id}, Threads: s.threadsHoldingLocked(postID)}, changed != add, true
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
	s.reactLocked(ch, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji, CreateAt: s.now().UnixMilli()}, mine)
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
