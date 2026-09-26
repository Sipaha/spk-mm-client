package api

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/store"
)

// DownloadView is an entry of the browser-like downloads list.
type DownloadView struct {
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"`
	FileID   string `json:"file_id"`
	// Name is the saved file's name once done (it may have got a " (N)"),
	// else the server's (sanitized) name.
	Name       string `json:"name"`
	Path       string `json:"path"` // "" until done
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	StartedAt  int64  `json:"started_at"`  // unix ms; raised when the file is asked for again
	FinishedAt int64  `json:"finished_at"` // unix ms, 0 while downloading
	State      string `json:"state"`       // downloading | done | failed
	Error      string `json:"error"`       // error code of a failed download ("interrupted": the app quit)
	Received   int64  `json:"received"`    // bytes so far (downloading), size when done
	// Exists: done and the file is still there with its size — else the UI
	// shows "file deleted" and no actions.
	Exists bool `json:"exists"`
	// Openable: OpenDownload hands this type to the system (the same
	// allowlist as OpenFile); otherwise the UI offers only "Show in folder".
	Openable bool `json:"openable"`
}

// Revealer shows a file in the system file manager (desktop: D-Bus
// FileManager1.ShowItems); it must return within a couple of seconds.
type Revealer func(ctx context.Context, path string) error

// SetRevealer sets how "Show in folder" asks the file manager; nil or a
// failure: the file's folder is opened with the file opener.
func (s *Service) SetRevealer(fn Revealer) {
	s.mu.Lock()
	s.reveal = fn
	s.mu.Unlock()
}

// progressEvery is the shortest gap between two progress events of one
// download (~4 a second).
const progressEvery = 250 * time.Millisecond

// meter counts the bytes of a download and reports the running total at
// most once per every (the first write at once).
type meter struct {
	every  time.Duration
	now    func() time.Time
	report func(received int64)
	n      int64
	last   time.Time
}

func (m *meter) Write(p []byte) (int, error) {
	m.n += int64(len(p))
	if t := m.now(); m.last.IsZero() || t.Sub(m.last) >= m.every {
		m.last = t
		m.report(m.n)
	}
	return len(p), nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

func (s *Service) downloadsChanged(payload map[string]any) {
	s.emit(EventDownloadsChanged, payload)
}

// startDownload lists a download in progress; 0: it could not be recorded
// (the download goes on unlisted).
func (s *Service) startDownload(ctx context.Context, srv int64, fileID string, info model.FileInfo) int64 {
	id, err := s.st.AddDownload(context.WithoutCancel(ctx), store.Download{ServerID: srv, FileID: fileID,
		Name: sanitizeName(info.Name), Size: info.Size, Mime: info.MimeType, StartedAt: nowMs()})
	if err != nil {
		slog.Warn("could not list the download", "srv", srv, "file", fileID, "err", err)
		return 0
	}
	s.filesMu.Lock()
	s.progress[id] = 0
	s.filesMu.Unlock()
	s.downloadsChanged(map[string]any{"id": id})
	return id
}

// meterFor reports a listed download's progress to the UI.
func (s *Service) meterFor(dl int64) *meter {
	if dl == 0 {
		return nil
	}
	return &meter{every: progressEvery, now: time.Now, report: func(n int64) {
		s.filesMu.Lock()
		s.progress[dl] = n
		s.filesMu.Unlock()
		s.downloadsChanged(map[string]any{"id": dl, "received": n})
	}}
}

// finishDownload records the result: done at path, or failed with the
// code of err.
func (s *Service) finishDownload(ctx context.Context, dl int64, path string, err error) {
	if dl == 0 {
		return
	}
	s.filesMu.Lock()
	delete(s.progress, dl)
	s.filesMu.Unlock()
	code, state := "", store.DownloadDone
	if err != nil {
		code, state = CodeInternal, store.DownloadFailed
		var ce *CodedError
		if errors.As(err, &ce) {
			code = ce.Code
		}
	}
	if ferr := s.st.FinishDownload(context.WithoutCancel(ctx), dl, path, code, nowMs()); ferr != nil {
		slog.Warn("could not record the download's result", "download", dl, "err", ferr)
	}
	s.downloadsChanged(map[string]any{"id": dl, "state": state})
}

// raiseDownload moves the entry of a file saved earlier this session to
// the top, or lists it anew if the user removed it; returns its entry.
func (s *Service) raiseDownload(ctx context.Context, dl, srv int64, fileID string, info model.FileInfo, path string) int64 {
	if dl != 0 {
		ok, err := s.st.RaiseDownload(context.WithoutCancel(ctx), dl, nowMs())
		if err != nil {
			slog.Warn("could not raise the download", "download", dl, "err", err)
			return dl
		}
		if ok {
			s.downloadsChanged(map[string]any{"id": dl})
			return dl
		}
	}
	dl = s.startDownload(ctx, srv, fileID, info)
	s.finishDownload(ctx, dl, path, nil)
	return dl
}

// fileThere: the saved file is still in place, unchanged in size.
func fileThere(path string, size int64) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() == size
}

// Downloads implements API: the list, newest first. Read only when the UI
// asks; one os.Stat per finished entry (at most store.MaxDownloads).
func (s *Service) Downloads(ctx context.Context) ([]DownloadView, error) {
	list, err := s.st.ListDownloads(ctx)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	s.filesMu.Lock()
	received := make(map[int64]int64, len(s.progress))
	for id, n := range s.progress {
		received[id] = n
	}
	s.filesMu.Unlock()
	out := make([]DownloadView, 0, len(list))
	for _, d := range list {
		v := DownloadView{ID: d.ID, ServerID: d.ServerID, FileID: d.FileID, Name: d.Name, Path: d.Path, Size: d.Size,
			Mime: d.Mime, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt, State: d.State, Error: d.Error}
		switch d.State {
		case store.DownloadRunning:
			v.Received = received[d.ID]
		case store.DownloadDone:
			v.Received = d.Size
			v.Name = filepath.Base(d.Path)
			v.Exists = fileThere(d.Path, d.Size)
		}
		v.Openable = openable(v.Name)
		out = append(out, v)
	}
	return out, nil
}

// savedDownload is a finished entry whose file is still there.
func (s *Service) savedDownload(ctx context.Context, id int64) (store.Download, error) {
	d, err := s.st.GetDownload(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Download{}, coded(CodeNotFound, nil)
	}
	if err != nil {
		return store.Download{}, coded(CodeInternal, err)
	}
	if d.State != store.DownloadDone || !fileThere(d.Path, d.Size) {
		return store.Download{}, coded(CodeNoFile, nil)
	}
	return d, nil
}

// OpenDownload implements API: hands a listed file to the system under the
// same allowlist as OpenFile; false: not an openable type (or no opener).
func (s *Service) OpenDownload(ctx context.Context, id int64) (bool, error) {
	d, err := s.savedDownload(ctx, id)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	openFn := s.fileOpen
	s.mu.Unlock()
	if openFn == nil || !openable(d.Path) {
		return false, nil
	}
	if err := openFn(d.Path); err != nil {
		slog.Warn("could not open file", "path", d.Path, "err", err)
		return false, nil
	}
	return true, nil
}

// RevealDownload implements API: shows a listed file in the file manager,
// or, if that fails, opens its folder.
func (s *Service) RevealDownload(ctx context.Context, id int64) error {
	d, err := s.savedDownload(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	reveal, openFn := s.reveal, s.fileOpen
	s.mu.Unlock()
	if reveal != nil {
		err := reveal(ctx, d.Path)
		if err == nil {
			return nil
		}
		slog.Info("file manager did not show the file; opening its folder", "err", err)
	}
	if openFn == nil {
		return coded(CodeInternal, errors.New("no file opener"))
	}
	if err := openFn(filepath.Dir(d.Path)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}

// RemoveDownload implements API: drops a finished or failed entry (the file
// stays); a download in progress is kept.
func (s *Service) RemoveDownload(ctx context.Context, id int64) error {
	if err := s.st.RemoveDownload(ctx, id); err != nil {
		return coded(CodeInternal, err)
	}
	s.downloadsChanged(map[string]any{"id": id})
	return nil
}

// ClearDownloads implements API: drops every finished and failed entry.
func (s *Service) ClearDownloads(ctx context.Context) error {
	if err := s.st.ClearDownloads(ctx); err != nil {
		return coded(CodeInternal, err)
	}
	s.downloadsChanged(nil)
	return nil
}
