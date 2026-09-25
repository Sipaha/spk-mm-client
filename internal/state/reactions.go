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
	return "", false
}

// ReactLocal applies our own reaction at once — the click lands before the
// server answers — and records it as the intent for the post+emoji; false
// if the post is not in memory.
func (s *Server) ReactLocal(postID, emoji string, add bool) (Change, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}, false
	}
	now := s.now()
	if len(s.intents) >= maxIntents {
		for k, in := range s.intents {
			if now.After(in.until) {
				delete(s.intents, k)
			}
		}
	}
	s.intents[intentKey(postID, emoji)] = intent{add: add, until: now.Add(intentTTL)}
	s.reactLocked(ch, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji, CreateAt: now.UnixMilli()}, add)
	return Change{Channels: []string{ch}}, true
}

// UndoReactLocal rolls back a ReactLocal the server refused.
func (s *Server) UndoReactLocal(postID, emoji string, add bool) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.intents, intentKey(postID, emoji))
	ch, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}
	}
	s.reactLocked(ch, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji}, !add)
	return Change{Channels: []string{ch}}
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
	if s.now().After(in.until) || in.add == add {
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
// sorted by usageCount ascending. Returns the preference to save.
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
	b, _ := json.Marshal(list)
	p := model.Preference{UserID: s.me.ID, Category: "recent_emojis", Name: s.me.ID, Value: string(b)}
	s.prefs[prefKey{p.Category, p.Name}] = p.Value
	s.dirty.meta = true
	return p
}
