package api

import (
	"context"
	"errors"

	"github.com/spk/spk-mm-client/internal/mmsync"
)

// Jump to a post and the history around it (spec «Поиск», Секция 1). The
// UI passes ids only, checked here; Go makes every request.

// JumpDTO is where a jump landed (in_feed=false: a collapsed reply — the
// UI opens its thread).
type JumpDTO = mmsync.JumpResult

func (s *Service) JumpToPost(ctx context.Context, id int64, channelID, postID string) (JumpDTO, error) {
	if !validID(channelID) || !validID(postID) {
		return JumpDTO{}, coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return JumpDTO{}, err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	res, err := w.JumpTo(rctx, channelID, postID)
	if err != nil {
		return JumpDTO{}, histError(err)
	}
	return res, nil
}

func (s *Service) LoadNewer(ctx context.Context, id int64, channelID string) error {
	if !validID(channelID) {
		return coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	_, err = w.LoadNewer(rctx, channelID)
	return histError(err)
}

func (s *Service) RetryRevalidation(ctx context.Context, id int64, channelID string) error {
	if !validID(channelID) {
		return coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return histError(w.RetryRevalidation(rctx, channelID))
}

// histError: a history operation's error as a code — 404, 403, a
// cancelled or superseded navigation and the rest (actionError: 401,
// network) apart.
func histError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mmsync.ErrPostGone):
		return coded(CodePostGone, err)
	case errors.Is(err, mmsync.ErrForbidden):
		return coded(CodeForbidden, err)
	case errors.Is(err, mmsync.ErrWrongChannel):
		return coded(CodeInvalidArgument, err)
	case errors.Is(err, mmsync.ErrNoChannel):
		return coded(CodeNoChannel, nil)
	case errors.Is(err, mmsync.ErrNoProgress):
		return coded(CodeNoProgress, nil)
	case errors.Is(err, mmsync.ErrSuperseded), errors.Is(err, context.Canceled):
		return coded(CodeCancelled, err)
	}
	return actionError(err)
}
