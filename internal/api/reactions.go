package api

import "context"

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

func (s *Service) EmojiInfo(ctx context.Context, id int64) (EmojiDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return EmojiDTO{}, err
	}
	st := w.State()
	return EmojiDTO{Recent: st.RecentEmojis(), Custom: st.CustomEmojiNames(), CustomEnabled: st.CustomEmojiEnabled()}, nil
}
