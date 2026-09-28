package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/state"
)

// worker returns the sync worker of a signed-in server.
func (s *Service) worker(ctx context.Context, id int64) (*mmsync.Worker, error) {
	if m := s.manager(); m != nil {
		if w := m.Worker(id); w != nil {
			return w, nil
		}
	}
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if !srv.SignedIn() {
		return nil, coded(CodeNotSignedIn, nil)
	}
	return nil, coded(CodeInternal, errors.New("sync is not running"))
}

// writer is worker for actions that write: a dead session fails fast.
func (s *Service) writer(ctx context.Context, id int64) (*mmsync.Worker, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.Status() == mmsync.StatusNeedsReauth {
		return nil, coded(CodeSessionExpired, nil)
	}
	return w, nil
}

func actionError(err error) error {
	var re *rest.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mmsync.ErrEmptyMessage):
		return coded(CodeEmptyMessage, nil)
	case errors.Is(err, mmsync.ErrNoAttachments):
		return coded(CodeAttachmentsDisabled, nil)
	case errors.Is(err, mmsync.ErrNoPost):
		return coded(CodeNoPost, nil)
	case errors.As(err, &re) && re.ID == "app.reaction.save.save.too_many_reactions":
		return coded(CodeTooManyReactions, err)
	case errors.As(err, &re) && re.Status == http.StatusUnauthorized:
		return coded(CodeSessionExpired, err)
	case errors.As(err, &re) && re.Status == http.StatusForbidden:
		return coded(CodeForbidden, err)
	case rest.IsNetwork(err):
		return coded(CodeUnreachable, err)
	}
	return coded(CodeInternal, err)
}

func (s *Service) Sidebar(ctx context.Context, id int64, teamID string) (SidebarDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return SidebarDTO{}, err
	}
	return w.State().Sidebar(teamID), nil
}

func (s *Service) OpenChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return ChannelDTO{}, err
	}
	if _, ok := w.State().ChannelView(channelID); !ok {
		return ChannelDTO{}, coded(CodeNoChannel, nil)
	}
	s.mu.Lock()
	switched := s.active != id
	s.active = id
	s.mu.Unlock()
	// Open first, focus after: a worker that was in the background is still
	// unfocused here, so OpenChannel only makes channelID active; focusing
	// it earlier would mark its previously active channel read unseen.
	v, _ := w.OpenChannel(channelID)
	if switched {
		s.applyFocus()
	}
	return v, nil
}

func (s *Service) GetChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return ChannelDTO{}, err
	}
	v, ok := w.State().ChannelView(channelID)
	if !ok {
		return ChannelDTO{}, coded(CodeNoChannel, nil)
	}
	return v, nil
}

func (s *Service) LoadOlder(ctx context.Context, id int64, channelID string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.LoadOlder(rctx, channelID))
}

// SendPost implements API. Attachments move from the channel's composer to
// the pending post at once (all or none); the post is created once they
// are uploaded.
func (s *Service) SendPost(ctx context.Context, id int64, channelID, message string, attachmentIDs []string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	return s.sendWith(id, w, channelID, "", message, attachmentIDs)
}

// SendReply implements API. rootID must be a thread this server's thread
// cache holds for channelID (state.Server.ThreadHeld) — the panel that
// offers "reply" always has it open; otherwise no_post. Attachments move
// from the thread's composer (channelID, rootID) exactly as SendPost's
// move from the channel's: attach.Store.Take is keyed by (channel, root),
// so a thread's attachments can never reach the channel's post, or the
// reverse.
func (s *Service) SendReply(ctx context.Context, id int64, channelID, rootID, message string, attachmentIDs []string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	if rootID == "" || !w.State().ThreadHeld(channelID, rootID) {
		return coded(CodeNoPost, nil)
	}
	return s.sendWith(id, w, channelID, rootID, message, attachmentIDs)
}

// sendWith is SendPost/SendReply's shared body once the target is
// checked: rootID "" sends to the channel, else to that thread.
func (s *Service) sendWith(id int64, w *mmsync.Worker, channelID, rootID, message string, attachmentIDs []string) error {
	send := func(files ...state.FileView) error {
		if rootID == "" {
			return w.Send(channelID, message, files...)
		}
		return w.SendReply(channelID, rootID, message, files...)
	}
	if len(attachmentIDs) == 0 {
		return actionError(send())
	}
	if s.att == nil {
		return coded(CodeNotFound, nil)
	}
	taken, err := s.att.Take(id, channelID, rootID, attachmentIDs)
	if err != nil {
		return attachError(err)
	}
	files := make([]state.FileView, 0, len(taken))
	for _, a := range taken {
		files = append(files, s.stagedFile(a))
	}
	if err := send(files...); err != nil {
		postFiles{s}.Release(attachmentIDs)
		return actionError(err)
	}
	return nil
}

func (s *Service) RetryPost(ctx context.Context, id int64, channelID, pendingID string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	w.Retry(channelID, pendingID)
	return nil
}

func (s *Service) DiscardPost(ctx context.Context, id int64, channelID, pendingID string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	w.Discard(channelID, pendingID)
	return nil
}

func (s *Service) EditPost(ctx context.Context, id int64, postID, message string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.Edit(rctx, postID, message))
}

func (s *Service) DeletePost(ctx context.Context, id int64, postID string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.Delete(rctx, postID))
}

func (s *Service) MarkUnread(ctx context.Context, id int64, postID string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.MarkUnread(rctx, postID))
}

func (s *Service) SetPostSaved(ctx context.Context, id int64, postID string, saved bool) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.SetSaved(rctx, postID, saved))
}

func (s *Service) SaveDraft(ctx context.Context, id int64, channelID, text string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	w.SaveDraft(channelID, text)
	return nil
}

// SaveThreadDraft implements API: SaveDraft for a reply. rootID alone
// (channel-less — the thread cache's key is already the root) must be
// held (state.Server.ThreadHeld), else no_post: a draft for a root that
// was never opened, or evicted since, has no panel to show it in.
func (s *Service) SaveThreadDraft(ctx context.Context, id int64, rootID, text string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	if !w.State().ThreadHeld("", rootID) {
		return coded(CodeNoPost, nil)
	}
	w.State().SetThreadDraft(rootID, text)
	return nil
}

// OpenURL opens a link from a message in the system browser; only web and
// mail links — never file://, javascript: or custom schemes.
func (s *Service) OpenURL(_ context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return coded(CodeInvalidURL, err)
	}
	switch {
	case (u.Scheme == "http" || u.Scheme == "https") && u.Host != "":
	case u.Scheme == "mailto" && u.Opaque != "":
	default:
		return coded(CodeInvalidURL, nil)
	}
	if err := s.open(u.String()); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
