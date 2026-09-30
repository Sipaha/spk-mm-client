package api

import (
	"context"
	"errors"
)

// OpenThread implements API. The worker must be running (the thread is
// read by it); the view may still be loading — thread_changed follows.
func (s *Service) OpenThread(ctx context.Context, id int64, channelID, rootID string) (ThreadDTO, error) {
	if rootID == "" {
		return ThreadDTO{}, coded(CodeNoPost, nil)
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return ThreadDTO{}, err
	}
	v, ok := w.OpenThread(channelID, rootID)
	if !ok {
		return ThreadDTO{}, coded(CodeNoChannel, nil)
	}
	return v, nil
}

func (s *Service) OpenThreadAt(ctx context.Context, id int64, channelID, rootID, replyID string) (ThreadDTO, error) {
	if !validID(channelID) || !validID(rootID) || !validID(replyID) {
		return ThreadDTO{}, coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return ThreadDTO{}, err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	v, err := w.OpenThreadAt(rctx, channelID, rootID, replyID)
	if err != nil {
		return ThreadDTO{}, histError(err)
	}
	return v, nil
}

func (s *Service) LoadThreadFocus(ctx context.Context, id int64, rootID string, newer bool) error {
	if !validID(rootID) {
		return coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return histError(w.LoadThreadFocus(rctx, rootID, newer))
}

func (s *Service) RetryThreadRevalidation(ctx context.Context, id int64, rootID string) error {
	if !validID(rootID) {
		return coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return histError(w.RetryThreadRevalidation(rctx, rootID))
}

func (s *Service) GetThread(ctx context.Context, id int64, rootID string) (ThreadDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return ThreadDTO{}, err
	}
	v, ok := w.State().ThreadView(rootID)
	if !ok {
		return ThreadDTO{}, coded(CodeNoPost, nil)
	}
	return v, nil
}

func (s *Service) CloseThread(ctx context.Context, id int64) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	w.CloseThread()
	return nil
}

func (s *Service) LoadOlderReplies(ctx context.Context, id int64, rootID string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.LoadOlderReplies(rctx, rootID))
}
