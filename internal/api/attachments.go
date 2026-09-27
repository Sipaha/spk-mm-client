package api

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spk/spk-mm-client/internal/attach"
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

// AttachmentView is a file attached to a channel's next message.
type AttachmentView = attach.Attachment

var _ media.Staged = (*Service)(nil)

// EnableAttachments turns attachments on, spooling into dir; spools left
// by a previous run are swept in the background (startup is not delayed).
// Call it once, before Start.
func (s *Service) EnableAttachments(dir string) {
	s.att = attach.New(attach.Options{Dir: dir, Backend: attachBackend{s}, OnChange: s.onAttachments})
}

// attachBackend gives the attachment store the servers' limits and live
// uploaders.
type attachBackend struct{ s *Service }

func (b attachBackend) Limits(srv int64) (attach.Limits, error) {
	m := b.s.manager()
	if m == nil {
		return attach.Limits{}, coded(CodeNotSignedIn, nil)
	}
	w := m.Worker(srv)
	if w == nil {
		return attach.Limits{}, coded(CodeNotSignedIn, nil)
	}
	return attach.Limits{Enabled: w.State().FileAttachmentsEnabled(), MaxFileSize: w.State().MaxFileSize()}, nil
}

func (b attachBackend) Uploader(srv int64) attach.Uploader {
	w := b.s.running(srv)
	if w == nil {
		return nil
	}
	return uploader{w: w, s: b.s}
}

// uploader sends through the worker's REST client (its token and the rate
// limiter shared with sync) on the transfer HTTP client: no whole-request
// timeout, the store's stall timer bounds it.
type uploader struct {
	w *mmsync.Worker
	s *Service
}

func (u uploader) UploadFile(ctx context.Context, channelID, filename, clientID string, body io.Reader, size int64, progress func(int64)) (model.FileInfo, error) {
	return u.w.REST().WithHTTPClient(u.s.transfer).UploadFile(ctx, channelID, filename, clientID, body, size, progress)
}

func (u uploader) CheckAuth(err error) { u.w.CheckAuth(err) }

// onAttachments pushes a channel's attachments to the UI, coalesced like
// the other events; progress comes at most ~4 times a second per upload.
func (s *Service) onAttachments(srv int64, ch string) {
	s.co.Schedule(fmt.Sprintf("attachments/%d/%s", srv, ch), func() {
		if att := s.att; att != nil {
			s.emit(EventAttachmentsChanged, map[string]any{"srv": srv, "ch": ch, "items": att.List(srv, ch)})
		}
	})
}

func attachError(err error) error {
	var ae *attach.Error
	var ce *CodedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		return ce
	case errors.As(err, &ae):
		return &CodedError{Code: ae.Code, Detail: err.Error()}
	}
	return coded(CodeInternal, err)
}

// attachTarget checks that attachments are on, the session can write and
// the channel is known.
func (s *Service) attachTarget(ctx context.Context, id int64, channelID string) error {
	if s.att == nil {
		return coded(CodeInternal, errors.New("attachments are not enabled"))
	}
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	if _, ok := w.State().ChannelView(channelID); !ok {
		return coded(CodeNoChannel, nil)
	}
	return nil
}

// AddAttachmentPath attaches a file on disk to a channel's next message.
// The path comes from Go only (a drop, the file dialog, the clipboard) —
// never from the UI.
func (s *Service) AddAttachmentPath(ctx context.Context, id int64, channelID, path string) (AttachmentView, error) {
	if err := s.attachTarget(ctx, id, channelID); err != nil {
		return AttachmentView{}, err
	}
	a, err := s.att.AddPath(id, channelID, path)
	return a, attachError(err)
}

// AddAttachmentBytes attaches bytes without a file (a pasted picture, a
// browser upload), spooled to disk, at most limit bytes (≤ 0: the server's
// MaxFileSize).
func (s *Service) AddAttachmentBytes(ctx context.Context, id int64, channelID, name, mimeType string, r io.Reader, limit int64) (AttachmentView, error) {
	if err := s.attachTarget(ctx, id, channelID); err != nil {
		return AttachmentView{}, err
	}
	a, err := s.att.AddBytes(id, channelID, name, mimeType, r, limit)
	return a, attachError(err)
}

// Attachments implements API.
func (s *Service) Attachments(_ context.Context, id int64, channelID string) ([]AttachmentView, error) {
	if s.att == nil {
		return []AttachmentView{}, nil
	}
	return s.att.List(id, channelID), nil
}

// attachmentOf is a server's attachment.
func (s *Service) attachmentOf(id int64, attachmentID string) error {
	if s.att == nil {
		return coded(CodeNotFound, nil)
	}
	if a, ok := s.att.Get(attachmentID); !ok || a.Server != id {
		return coded(CodeNotFound, nil)
	}
	return nil
}

// RemoveAttachment implements API.
func (s *Service) RemoveAttachment(_ context.Context, id int64, attachmentID string) error {
	if err := s.attachmentOf(id, attachmentID); err != nil {
		return err
	}
	return attachError(s.att.Remove(attachmentID))
}

// RetryAttachment implements API: a failed upload is sent again.
func (s *Service) RetryAttachment(ctx context.Context, id int64, attachmentID string) error {
	if err := s.attachmentOf(id, attachmentID); err != nil {
		return err
	}
	if _, err := s.writer(ctx, id); err != nil {
		return err
	}
	return attachError(s.att.Retry(attachmentID))
}

// StagedType implements media.Staged.
func (s *Service) StagedType(serverID int64, id string) (string, bool) {
	if s.att == nil {
		return "", false
	}
	a, ok := s.att.Get(id)
	if !ok || a.Server != serverID {
		return "", false
	}
	return a.Mime, true
}

// OpenStaged implements media.Staged.
func (s *Service) OpenStaged(serverID int64, id string) (io.ReadCloser, error) {
	if s.att == nil {
		return nil, errors.New("attachments are not enabled")
	}
	f, _, err := s.att.Open(serverID, id)
	if err != nil {
		return nil, err
	}
	return f, nil
}
