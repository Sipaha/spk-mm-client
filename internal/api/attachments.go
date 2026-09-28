package api

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"strings"

	"github.com/spk/spk-mm-client/internal/attach"
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/state"
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

// onAttachments pushes a (channel, root) composer's attachments to the UI,
// coalesced like the other events; progress comes at most ~4 times a
// second per upload. It also refreshes the progress/error shown on any
// pending post's staged files of this channel (an attachment stays
// tracked, and keeps notifying, after Take moves it from the composer to
// a post being sent) and, only when that actually changed something,
// tells the UI: thread_changed for root != "" (a reply's files only ever
// touch its own thread), channel_changed otherwise — the same coalesced
// path a post/window update uses.
func (s *Service) onAttachments(srv int64, ch, root string) {
	s.co.Schedule(fmt.Sprintf("attachments/%d/%s/%s", srv, ch, root), func() {
		if att := s.att; att != nil {
			s.emit(EventAttachmentsChanged, map[string]any{"server_id": srv, "channel_id": ch, "root_id": root, "items": att.List(srv, ch, root)})
		}
	})
	m := s.manager()
	if m == nil || s.att == nil {
		return
	}
	w := m.Worker(srv)
	if w == nil {
		return
	}
	changed := w.State().RefreshPendingProgress(ch, func(id string) (state.FileProgress, bool) {
		a, ok := s.att.Get(id)
		if !ok || a.Server != srv {
			return state.FileProgress{}, false
		}
		return state.FileProgress{State: string(a.State), Sent: a.Sent, Error: a.Error}, true
	})
	if !changed {
		return
	}
	if root != "" {
		s.co.Schedule(fmt.Sprintf("thread/%d/%s", srv, root), func() {
			s.emit(EventThreadChanged, map[string]any{"server_id": srv, "root_id": root})
		})
		return
	}
	s.co.Schedule(fmt.Sprintf("channel/%d/%s", srv, ch), func() {
		s.emit(EventChannelChanged, map[string]any{"server_id": srv, "channel_id": ch})
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

// attachTarget checks that attachments are on, the session can write, the
// channel is known and — for a reply's composer (rootID != "") — that
// rootID is a thread this server's thread cache holds for channelID
// (state.Server.ThreadHeld): an unknown root (never opened, evicted since)
// is refused as no_post rather than silently attaching to nothing a panel
// can show.
func (s *Service) attachTarget(ctx context.Context, id int64, channelID, rootID string) error {
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
	if rootID != "" && !w.State().ThreadHeld(channelID, rootID) {
		return coded(CodeNoPost, nil)
	}
	return nil
}

// AddAttachmentPath attaches a file on disk to (channelID, rootID)'s
// composer. The path comes from Go only (a drop, the file dialog, the
// clipboard) — never from the UI.
func (s *Service) AddAttachmentPath(ctx context.Context, id int64, channelID, rootID, path string) (AttachmentView, error) {
	if err := s.attachTarget(ctx, id, channelID, rootID); err != nil {
		return AttachmentView{}, err
	}
	a, err := s.att.AddPath(id, channelID, rootID, path)
	return a, attachError(err)
}

// AddAttachmentBytes attaches bytes without a file (a pasted picture, a
// browser upload) to (channelID, rootID)'s composer, spooled to disk, at
// most limit bytes (≤ 0: the server's MaxFileSize).
func (s *Service) AddAttachmentBytes(ctx context.Context, id int64, channelID, rootID, name, mimeType string, r io.Reader, limit int64) (AttachmentView, error) {
	if err := s.attachTarget(ctx, id, channelID, rootID); err != nil {
		return AttachmentView{}, err
	}
	a, err := s.att.AddBytes(id, channelID, rootID, name, mimeType, r, limit)
	return a, attachError(err)
}

// AttachmentSizeLimit is the largest file a server takes: its
// MaxFileSize, attach.DefaultMaxFileSize while that is not known.
func (s *Service) AttachmentSizeLimit(id int64) int64 {
	if lim, err := (attachBackend{s}).Limits(id); err == nil && lim.MaxFileSize > 0 {
		return lim.MaxFileSize
	}
	return attach.DefaultMaxFileSize
}

// Attachments implements API.
func (s *Service) Attachments(_ context.Context, id int64, channelID, rootID string) ([]AttachmentView, error) {
	if s.att == nil {
		return []AttachmentView{}, nil
	}
	return s.att.List(id, channelID, rootID), nil
}

// attachmentOf is an attachment in a server's composer (one taken by a
// post being sent is the post's: RetryPost, DiscardPost).
func (s *Service) attachmentOf(id int64, attachmentID string) error {
	if s.att == nil {
		return coded(CodeNotFound, nil)
	}
	if a, ok := s.att.Get(attachmentID); !ok || a.Server != id || a.Taken {
		return coded(CodeNotFound, nil)
	}
	return nil
}

// postFiles gives the sync workers the uploads of the posts they send.
type postFiles struct{ s *Service }

var _ mmsync.Files = postFiles{}

var errNoAttachments = errors.New("attachments are not enabled")

func (p postFiles) Wait(ctx context.Context, ids []string) ([]string, error) {
	if p.s.att == nil {
		return nil, errNoAttachments
	}
	return p.s.att.Wait(ctx, ids)
}

func (p postFiles) Retry(ids []string) {
	if p.s.att == nil {
		return
	}
	for _, id := range ids {
		_ = p.s.att.Retry(id) // a gone one fails the post's Wait
	}
}

func (p postFiles) Release(ids []string) {
	if p.s.att == nil {
		return
	}
	for _, id := range ids {
		_ = p.s.att.Remove(id) // gone already: nothing to let go of
	}
}

// stagedFile shows an attachment of a post being sent like a server file:
// its picture comes from /media/<srv>/staged/<id>; a raster picture's size
// is read from its header so the feed's box is final before it loads.
func (s *Service) stagedFile(a attach.Attachment) state.FileView {
	fv := state.FileView{ID: a.ID, Name: a.Name, Ext: strings.ToLower(strings.TrimPrefix(filepath.Ext(a.Name), ".")),
		Size: a.Size, Mime: a.Mime, Staged: true, State: string(a.State), Sent: a.Sent, Error: a.Error}
	if !media.IsRaster(a.Mime) {
		return fv
	}
	f, _, err := s.att.Open(a.Server, a.ID)
	if err != nil {
		return fv
	}
	defer f.Close()
	if c, _, err := image.DecodeConfig(f); err == nil {
		fv.Width, fv.Height = c.Width, c.Height
	}
	return fv
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
