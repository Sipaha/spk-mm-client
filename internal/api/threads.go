package api

import "context"

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
