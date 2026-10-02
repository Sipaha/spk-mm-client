package api

import (
	"context"
	"errors"

	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/state"
)

type PinnedPostDTO = state.PostView

func (s *Service) PinnedPosts(ctx context.Context, id int64, channelID string) ([]PinnedPostDTO, error) {
	if !validID(channelID) {
		return nil, coded(CodeInvalidArgument, errors.New("channel id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return nil, err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	posts, err := w.PinnedPosts(rctx, channelID)
	if errors.Is(err, mmsync.ErrUnknownChannel) {
		return nil, coded(CodeNoChannel, err)
	}
	if err != nil {
		return nil, searchError(err)
	}
	return posts, nil
}
