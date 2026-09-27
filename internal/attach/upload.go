package attach

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

// Uploader sends files to a live server: rest.Client.UploadFile on the
// transfer HTTP client, and the worker's 401 handling.
type Uploader interface {
	UploadFile(ctx context.Context, channelID, filename, clientID string, body io.Reader, size int64, progress func(sent int64)) (model.FileInfo, error)
	// CheckAuth takes an upload's error: a 401 asks for a new sign-in.
	CheckAuth(err error)
}

var (
	errChanged = errors.New("attach: the file changed after it was attached")
	errStalled = errors.New("attach: no bytes sent for too long")
)

// pump starts queued uploads of a live server, up to Parallel at a time.
func (s *Store) pump(srv int64) {
	up := s.o.Backend.Uploader(srv) // outside the lock: the backend has locks of its own
	if up == nil {
		return
	}
	var started []*item
	s.mu.Lock()
	q := s.queues[srv]
	for !s.closed && q != nil && !q.paused && q.running < s.o.Parallel && len(q.pending) > 0 {
		it := q.pending[0]
		q.pending = q.pending[1:]
		if s.items[it.ID] != it || it.State != StateStaged {
			continue // removed, or queued twice
		}
		ctx, stop := context.WithCancelCause(s.life)
		it.State, it.Sent, it.Error, it.stop, it.shown = StateUploading, 0, "", stop, time.Time{}
		q.running++
		s.wg.Add(1)
		go s.run(ctx, q, it, it.gen, up)
		started = append(started, it)
	}
	if len(started) > 0 {
		s.broadcastLocked()
	}
	s.mu.Unlock()
	for _, it := range started {
		s.notify(it.Server, it.Channel)
	}
}

// run uploads one attachment and records the outcome, unless the upload
// was abandoned meanwhile (gen changed).
func (s *Store) run(ctx context.Context, q *queue, it *item, gen int, up Uploader) {
	defer s.wg.Done()
	info, err := s.send(ctx, it, gen, up)
	if err != nil {
		up.CheckAuth(err)
	}
	s.mu.Lock()
	q.running--
	current := s.items[it.ID] == it && it.gen == gen
	if current {
		it.stop = nil
		if err == nil {
			it.State, it.FileID, it.Sent = StateUploaded, info.ID, it.Size
		} else {
			it.State, it.Error = StateFailed, codeFor(err)
		}
		s.broadcastLocked()
	}
	s.mu.Unlock()
	if current {
		s.notify(it.Server, it.Channel)
	}
	s.pump(it.Server)
}

// send streams the file after checking it is the one attached; progress
// is recorded as it goes, and an upload that sends nothing for Stall is
// cancelled.
func (s *Store) send(ctx context.Context, it *item, gen int, up Uploader) (model.FileInfo, error) {
	f, err := os.Open(it.path)
	if err != nil {
		return model.FileInfo{}, errChanged
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != it.Size || !fi.ModTime().Equal(it.mtime) {
		return model.FileInfo{}, errChanged
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(s.o.Stall, func() { cancel(errStalled) })
	defer watchdog.Stop()
	info, err := up.UploadFile(ctx, it.Channel, it.Name, it.ID, io.LimitReader(f, it.Size), it.Size, func(sent int64) {
		watchdog.Reset(s.o.Stall)
		s.progress(it, gen, sent)
	})
	if err != nil && errors.Is(context.Cause(ctx), errStalled) {
		return model.FileInfo{}, errStalled
	}
	return info, err
}

// progress records the bytes sent and reports them at most every
// ProgressEvery (the first at once).
func (s *Store) progress(it *item, gen int, sent int64) {
	now := time.Now()
	s.mu.Lock()
	if s.items[it.ID] != it || it.gen != gen {
		s.mu.Unlock()
		return
	}
	it.Sent = sent
	due := it.shown.IsZero() || now.Sub(it.shown) >= s.o.ProgressEvery
	if due {
		it.shown = now
	}
	s.mu.Unlock()
	if due {
		s.notify(it.Server, it.Channel)
	}
}

// codeFor maps an upload error to the code the UI shows.
func codeFor(err error) string {
	var re *rest.Error
	switch {
	case errors.Is(err, errChanged):
		return CodeChanged
	case errors.Is(err, errStalled):
		return CodeUnreachable
	case errors.As(err, &re) && re.ID == "api.file.attachments.disabled.app_error":
		return CodeDisabled
	case rest.IsTooLarge(err):
		return CodeTooLarge
	case errors.As(err, &re) && re.Status == http.StatusUnauthorized:
		return CodeSessionExpired
	case errors.As(err, &re) && re.Status == http.StatusForbidden:
		return CodeForbidden
	case rest.IsNetwork(err):
		return CodeUnreachable
	}
	return CodeInternal
}
