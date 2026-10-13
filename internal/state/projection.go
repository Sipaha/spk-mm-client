package state

import (
	"reflect"
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// ChannelPatch is an acknowledged-baseline projection. On a lost/out-of-order
// baseline Reset is a full snapshot; otherwise Posts contains only changed
// rows, Removed deleted IDs, and Order appears only when membership/order moved.
// Headers are always current (read markers, history gaps, drafts, and so on).
type ChannelPatch struct {
	View     ChannelView `json:"view"`
	Base     uint64      `json:"base"`
	Revision uint64      `json:"revision"`
	Reset    bool        `json:"reset"`
	Posts    []PostView  `json:"posts"`
	Removed  []string    `json:"removed,omitempty"`
	Order    []string    `json:"order,omitempty"`
}

type projectedPost struct {
	view     PostView
	metadata *model.PostMetadata
	seen     uint64
}

type channelProjection struct {
	channel  string
	revision uint64
	posts    map[string]*projectedPost
	order    []string
}

func (s *Server) HasChannel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.chans[id]
	return ok
}

func (s *Server) ChannelDelta(id string, base uint64) (ChannelPatch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chans[id] == nil {
		return ChannelPatch{}, false
	}
	cache := &s.projection
	reset := cache.channel != id || base == 0 || base != cache.revision
	if cache.channel != id {
		cache.channel = id
		cache.posts = make(map[string]*projectedPost)
		cache.order = nil
	}
	cache.revision++
	out := ChannelPatch{Base: base, Revision: cache.revision, Reset: reset, Posts: []PostView{}}
	// Compare order as we walk, allocate only once it actually changes.
	index := 0
	orderChanged := reset
	var order []string
	emit := func(p PostView, metadata *model.PostMetadata) {
		old := cache.posts[p.ID]
		if old == nil {
			old = &projectedPost{}
			cache.posts[p.ID] = old
		}
		if reset || old.seen == 0 || !samePostView(old.view, p) {
			out.Posts = append(out.Posts, p)
			old.view = p
		}
		old.metadata, old.seen = metadata, cache.revision
		if !orderChanged && (index >= len(cache.order) || cache.order[index] != p.ID) {
			orderChanged = true
			order = append(make([]string, 0, max(len(cache.order), index+1)), cache.order[:index]...)
		}
		if orderChanged {
			order = append(order, p.ID)
		}
		index++
	}
	view, _ := s.channelViewLocked(id, emit)
	out.View = view
	for key, p := range cache.posts {
		if p.seen != cache.revision {
			out.Removed = append(out.Removed, key)
			delete(cache.posts, key)
		}
	}
	if !orderChanged && index != len(cache.order) {
		orderChanged = true
		order = slices.Clone(cache.order[:index])
	}
	if orderChanged {
		// A non-nil empty order must survive JSON (empty channels).
		out.Order = order
		cache.order = order
	}
	return out, true
}

// Avoid reflecting/boxing the large row for scalar comparison. Nested
// attachments originate from immutable post replacements; DeepEqual covers
// the rare rich payload, and files/reactions have comparable elements.
func samePostView(a, b PostView) bool {
	return a.ID == b.ID && a.UserID == b.UserID && a.Author == b.Author && a.RealAuthor == b.RealAuthor &&
		a.Icon == b.Icon && a.IconVersion == b.IconVersion && a.Webhook == b.Webhook && a.Avatar == b.Avatar &&
		a.Status == b.Status && a.RootID == b.RootID && a.Message == b.Message && a.CreateAt == b.CreateAt &&
		a.EditAt == b.EditAt && a.ReplyCount == b.ReplyCount && a.LastReplyAt == b.LastReplyAt && a.System == b.System &&
		a.Bot == b.Bot && a.Pending == b.Pending && a.Failed == b.Failed && a.PendingPostID == b.PendingPostID &&
		a.Saved == b.Saved && a.Ephemeral == b.Ephemeral && a.SystemAuthor == b.SystemAuthor && a.RootAuthor == b.RootAuthor &&
		a.RootSnippet == b.RootSnippet && slices.Equal(a.Files, b.Files) && slices.Equal(a.Reactions, b.Reactions) &&
		((len(a.Attachments) == 0 && len(b.Attachments) == 0) || reflect.DeepEqual(a.Attachments, b.Attachments))
}
