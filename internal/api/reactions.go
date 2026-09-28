package api

import (
	"context"

	"github.com/spk/spk-mm-client/internal/state"
)

// ReactionUsersDTO answers "who reacted with this emoji" for a reaction
// chip's tooltip/modal: everyone except me (the UI adds "You"), oldest
// reaction first. A Reactor with Name=="" is unknown/unfetched — counted
// in Unknown, shown as a placeholder.
type ReactionUsersDTO = state.ReactionUsersView

// Reactor is one row of ReactionUsersDTO.Users.
type Reactor = state.Reactor

// EmojiDTO feeds the emoji picker of one server.
type EmojiDTO struct {
	Recent        []string `json:"recent"` // most used first
	Custom        []string `json:"custom"` // custom emoji names, by name
	CustomEnabled bool     `json:"custom_enabled"`
}

func (s *Service) AddReaction(ctx context.Context, id int64, postID, emoji string) error {
	return s.react(ctx, id, postID, emoji, true)
}

func (s *Service) RemoveReaction(ctx context.Context, id int64, postID, emoji string) error {
	return s.react(ctx, id, postID, emoji, false)
}

func (s *Service) react(ctx context.Context, id int64, postID, emoji string, add bool) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.React(rctx, postID, emoji, add))
}

// ReactionUsers is a read — like GetThread/ChannelView, it works from state
// alone whenever nothing needs fetching, even needs_reauth (see
// mmsync.Worker.ReactionUsers, AGENTS.md); s.worker (not s.writer) reflects
// that.
func (s *Service) ReactionUsers(ctx context.Context, id int64, postID, emoji string) (ReactionUsersDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return ReactionUsersDTO{}, err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	v, err := w.ReactionUsers(rctx, postID, emoji)
	if err != nil {
		return ReactionUsersDTO{}, actionError(err)
	}
	return v, nil
}

func (s *Service) EmojiInfo(ctx context.Context, id int64) (EmojiDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return EmojiDTO{}, err
	}
	st := w.State()
	return EmojiDTO{Recent: st.RecentEmojis(), Custom: st.CustomEmojiNames(), CustomEnabled: st.CustomEmojiEnabled()}, nil
}
