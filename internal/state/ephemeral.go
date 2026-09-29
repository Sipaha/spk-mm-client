package state

import (
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

// Ephemeral posts (a slash command's answer, the ephemeral_message event):
// shown only to us, never stored by the server — so kept only here, apart
// from the windows (never in the snapshot, gone on restart like the
// webapp's), bounded to maxEphemeral per server, oldest first out; leaving
// a channel drops its own.
const maxEphemeral = 50

func (s *Server) onEphemeralLocked(ev ws.Event, eff *Effects) {
	p, err := ws.DecodePost(ev)
	if err != nil || p.ID == "" || s.chans[p.ChannelID] == nil {
		return
	}
	p.Type = model.PostTypeEphemeral
	s.ephemeral = slices.DeleteFunc(s.ephemeral, func(q model.Post) bool { return q.ID == p.ID })
	if len(s.ephemeral) >= maxEphemeral {
		s.ephemeral = slices.Delete(s.ephemeral, 0, len(s.ephemeral)-maxEphemeral+1)
	}
	s.ephemeral = append(s.ephemeral, p)
	eff.Channels = []string{p.ChannelID}
	if p.RootID != "" && s.threads[p.RootID] != nil {
		eff.Threads = []string{p.RootID}
	}
}

// withEphemeralLocked merges the ephemeral posts that belong in a list —
// keep says which — into posts (oldest first) by time.
func (s *Server) withEphemeralLocked(posts []model.Post, keep func(model.Post) bool) []model.Post {
	var add []model.Post
	for _, p := range s.ephemeral {
		if keep(p) {
			add = append(add, p)
		}
	}
	if len(add) == 0 {
		return posts
	}
	out := make([]model.Post, 0, len(posts)+len(add))
	out = append(out, posts...)
	out = append(out, add...)
	slices.SortStableFunc(out, func(a, b model.Post) int {
		switch {
		case a.CreateAt < b.CreateAt:
			return -1
		case a.CreateAt > b.CreateAt:
			return 1
		}
		return 0
	})
	return out
}

func (s *Server) forgetEphemeralLocked(channelID string) {
	s.ephemeral = slices.DeleteFunc(s.ephemeral, func(p model.Post) bool { return p.ChannelID == channelID })
}
