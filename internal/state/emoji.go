package state

import (
	"sort"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// SetCustomEmoji replaces the server's custom emoji (name → id).
func (s *Server) SetCustomEmoji(list []model.Emoji) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emoji = make(map[string]string, len(list))
	for _, e := range list {
		s.addEmojiLocked(e)
	}
}

func (s *Server) AddCustomEmoji(e model.Emoji) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEmojiLocked(e)
}

func (s *Server) addEmojiLocked(e model.Emoji) {
	if e.ID != "" && e.Name != "" && e.DeleteAt == 0 {
		s.emoji[e.Name] = e.ID
	}
}

func (s *Server) CustomEmojiID(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.emoji[name]
	return id, ok
}

// CustomEmojiNames lists the custom emoji for the picker, by name.
func (s *Server) CustomEmojiNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.emoji))
	for n := range s.emoji {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (s *Server) CustomEmojiEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.CustomEmoji
}
