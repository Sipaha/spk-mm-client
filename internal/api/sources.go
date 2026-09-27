package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/attach"
)

// Clipboard reads the system clipboard for AttachFromClipboard (desktop:
// GTK, asked on its main thread asynchronously — see
// internal/desktop/clipboard_gtk.go). Every method gives up when ctx ends.
type Clipboard interface {
	// TakePasteGesture reports (and uses up) a paste key — Ctrl+V or
	// Shift+Insert — the user pressed in the app's window just now, as
	// seen natively (not by the page). Page script can call
	// AttachFromClipboard whenever it likes; without this the clipboard
	// is not read.
	TakePasteGesture() bool
	// Targets are the formats the clipboard offers now.
	Targets(ctx context.Context) ([]string, error)
	// Contents of one target; the caller closes it.
	Contents(ctx context.Context, target string) (io.ReadCloser, error)
	// ImagePNG is the clipboard's picture as PNG, converted from whatever
	// image format the owner offers; the caller closes it.
	ImagePNG(ctx context.Context) (io.ReadCloser, error)
}

// FilePicker asks the user for files to attach (desktop: the native
// open-file dialog). It returns once the user closes it; none: cancelled.
type FilePicker interface {
	PickFiles(ctx context.Context) ([]string, error)
}

// SetClipboard sets where AttachFromClipboard reads from; nil (browser
// mode, where the page gets the pasted files itself): unsupported.
func (s *Service) SetClipboard(c Clipboard) {
	s.mu.Lock()
	s.clipboard = c
	s.mu.Unlock()
}

// SetFilePicker sets the dialog of PickAttachments; nil (browser mode, an
// <input type=file> there): unsupported.
func (s *Service) SetFilePicker(p FilePicker) {
	s.mu.Lock()
	s.picker = p
	s.mu.Unlock()
}

// clipboardTimeout bounds a whole paste (every clipboard request of it):
// an owner that never answers must not keep a paste waiting (GTK's own
// selection timeout is longer).
var clipboardTimeout = 2 * time.Second

// screenshotSeq guards nextScreenshotName's within-process de-duplication:
// GTK/Wayland screenshot names only go to the second, so two pastes in the
// same second would otherwise both spool as the same
// "Screenshot <date> <time>.png".
var (
	screenshotMu     sync.Mutex
	lastScreenshotAt string
	screenshotSeq    int
)

// nextScreenshotName returns "Screenshot <date> <time>.png" for now,
// unique within this process: a repeat within the same second gets
// " (2)", " (3)", … appended (like a browser's download manager), reset
// once the clock has moved past that second again.
func nextScreenshotName(now time.Time) string {
	screenshotMu.Lock()
	defer screenshotMu.Unlock()
	stamp := now.Format("2006-01-02 15-04-05")
	if stamp == lastScreenshotAt {
		screenshotSeq++
	} else {
		lastScreenshotAt = stamp
		screenshotSeq = 1
	}
	name := "Screenshot " + stamp
	if screenshotSeq > 1 {
		name += fmt.Sprintf(" (%d)", screenshotSeq)
	}
	return name + ".png"
}

// Clipboard targets AttachFromClipboard understands.
const (
	targetURIList     = "text/uri-list"
	targetGnomeCopied = "x-special/gnome-copied-files"
	targetPNG         = "image/png"
)

// maxFileListSize bounds a file list read from the clipboard.
const maxFileListSize = 1 << 20

// AttachFromClipboard implements API. Only right after a native paste key
// (TakePasteGesture; a paste from a context menu is not supported):
// files copied in a file manager are attached by path; otherwise a picture (PNG as is, any other format
// converted to PNG) is spooled as "Screenshot <date> <time>.png"; anything
// else attaches nothing. It returns how many were attached (they arrive
// with attachments_changed); an error says why (the first one) when some
// or all were refused.
func (s *Service) AttachFromClipboard(ctx context.Context, id int64, channelID string) (int, error) {
	s.mu.Lock()
	cb := s.clipboard
	s.mu.Unlock()
	if cb == nil {
		return 0, coded(CodeUnsupported, nil)
	}
	if !cb.TakePasteGesture() {
		return 0, coded(CodeNoPasteGesture, nil)
	}
	if err := s.attachTarget(ctx, id, channelID); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	defer cancel()
	targets, err := cb.Targets(ctx)
	if err != nil {
		return 0, coded(CodeClipboardFailed, err)
	}
	offered := map[string]bool{}
	image := ""
	for _, t := range targets {
		offered[t] = true
		if image == "" && strings.HasPrefix(t, "image/") {
			image = t
		}
	}
	var paths []string
	if offered[targetURIList] {
		paths = parseURIList(s.clipboardText(ctx, cb, targetURIList))
	}
	if len(paths) == 0 && offered[targetGnomeCopied] {
		paths = parseGnomeCopiedFiles(s.clipboardText(ctx, cb, targetGnomeCopied))
	}
	switch {
	case len(paths) > 0:
		return s.addPaths(id, channelID, paths)
	case image == "":
		return 0, nil
	}
	var r io.ReadCloser
	if offered[targetPNG] {
		r, err = cb.Contents(ctx, targetPNG)
	} else {
		r, err = cb.ImagePNG(ctx)
	}
	if err != nil {
		return 0, coded(CodeClipboardFailed, err)
	}
	defer r.Close()
	name := nextScreenshotName(time.Now())
	if _, err := s.att.AddBytes(id, channelID, name, "image/png", r, 0); err != nil {
		return 0, attachError(err)
	}
	return 1, nil
}

// clipboardText reads a small text target; a failure reads as empty.
func (s *Service) clipboardText(ctx context.Context, cb Clipboard, target string) string {
	r, err := cb.Contents(ctx, target)
	if err != nil {
		slog.Warn("clipboard target unreadable", "target", target, "err", err)
		return ""
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, maxFileListSize))
	if err != nil {
		return ""
	}
	return string(b)
}

// parseURIList gives the local paths of a text/uri-list (RFC 2483):
// file:// URIs with no host or localhost, percent-decoded; links and other
// schemes are left out.
func parseURIList(list string) []string {
	var out []string
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" {
			continue
		}
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			continue
		}
		if !strings.HasPrefix(u.Path, "/") || strings.ContainsRune(u.Path, 0) {
			continue
		}
		out = append(out, u.Path)
	}
	return out
}

// parseGnomeCopiedFiles reads x-special/gnome-copied-files ("copy" or
// "cut", then one URI a line); a cut is attached like a copy — nothing is
// ever moved.
func parseGnomeCopiedFiles(data string) []string {
	first, rest, _ := strings.Cut(data, "\n")
	switch strings.TrimSpace(first) {
	case "copy", "cut":
		data = rest
	}
	return parseURIList(data)
}

// PickAttachments implements API: the file dialog (only on the user's
// click — never at startup), then the chosen files by path.
func (s *Service) PickAttachments(ctx context.Context, id int64, channelID string) (int, error) {
	s.mu.Lock()
	p := s.picker
	s.mu.Unlock()
	if p == nil {
		return 0, coded(CodeUnsupported, nil)
	}
	if err := s.attachTarget(ctx, id, channelID); err != nil {
		return 0, err
	}
	if err := s.attachRoom(id, channelID); err != nil {
		return 0, err
	}
	paths, err := p.PickFiles(ctx)
	if err != nil {
		return 0, coded(CodeInternal, err)
	}
	return s.addPaths(id, channelID, paths)
}

// attachRoom refuses before a dialog opens what would be refused after
// it: attachments off on the server, or the channel full.
func (s *Service) attachRoom(id int64, channelID string) error {
	lim, err := attachBackend{s}.Limits(id)
	if err != nil {
		return err
	}
	if lim.MaxFileSize > 0 && !lim.Enabled {
		return coded(CodeAttachmentsDisabled, nil)
	}
	if len(s.att.List(id, channelID)) >= attach.MaxPerChannel {
		return coded(CodeTooMany, nil)
	}
	return nil
}

// AttachDropped attaches files dropped onto a channel (desktop: Wails'
// WindowFilesDropped). Nobody waits for the answer, so a refusal reaches
// the UI as attachment_refused.
//
// Threat model: page script (an XSS in rendered content) can call every
// binding, and Wails passes dropped paths through the page, so a drop can
// be forged. The desktop therefore passes here only paths a native GTK
// drop on the webview carried moments ago (internal/desktop dropGate);
// others are refused (not_dropped) before they get here. Staged files
// upload at once and page script could also call SendPost, so what
// remains is a file the user really dropped (or pasted, or picked) — the
// app's own data is refused even then (addPaths).
func (s *Service) AttachDropped(ctx context.Context, id int64, channelID string, paths []string) int {
	if err := s.attachTarget(ctx, id, channelID); err != nil {
		s.refused(id, channelID, err)
		return 0
	}
	n, err := s.addPaths(id, channelID, paths)
	if err != nil {
		s.refused(id, channelID, err)
	}
	return n
}

// DropRefused reports files of a drop refused before AttachDropped (not
// carried by the native drop, too many at once).
func (s *Service) DropRefused(id int64, channelID, code string) {
	slog.Info("dropped files refused", "server", id, "code", code)
	s.emit(EventAttachmentRefused, map[string]any{"server_id": id, "channel_id": channelID, "code": code})
}

func (s *Service) refused(id int64, channelID string, err error) {
	code := CodeInternal
	var ce *CodedError
	if errors.As(err, &ce) {
		code = ce.Code
	}
	slog.Info("dropped files refused", "server", id, "code", code, "err", err)
	s.emit(EventAttachmentRefused, map[string]any{"server_id": id, "channel_id": channelID, "code": code})
}

// ProtectDir sets the app's data directory (database with tokens, caches,
// spools): no file in it is ever staged from a path — not through a
// symlink either.
func (s *Service) ProtectDir(dir string) {
	s.mu.Lock()
	s.protected = dir
	s.mu.Unlock()
}

// addPaths attaches files by path in order (the caller checked the
// target). Symlinks are resolved first: a file is staged as its target
// (name included), and refused when that is in the app's data directory.
// A file that cannot be taken (a folder, empty, too large, app data) is
// skipped; a refusal that holds for the rest too (the channel is full,
// attachments are off) stops. It returns how many were attached and the
// first refusal.
func (s *Service) addPaths(id int64, channelID string, paths []string) (int, error) {
	s.mu.Lock()
	protected := s.protected
	s.mu.Unlock()
	n := 0
	var first error
	for _, p := range paths {
		err := s.addPath(id, channelID, p, protected)
		if err == nil {
			n++
			continue
		}
		if first == nil {
			first = err
		}
		var ce *CodedError
		if !errors.As(err, &ce) || (ce.Code != CodeNotAFile && ce.Code != CodeEmptyFile && ce.Code != CodeTooLarge && ce.Code != CodeAppData) {
			break
		}
	}
	return n, first
}

func (s *Service) addPath(id int64, channelID, path, protected string) error {
	if !filepath.IsAbs(path) {
		return coded(CodeNotAFile, fmt.Errorf("not an absolute path: %q", path))
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return coded(CodeNotAFile, err)
	}
	if within(resolved, protected) {
		return coded(CodeAppData, fmt.Errorf("%q is in the app's data directory", path))
	}
	_, err = s.att.AddPath(id, channelID, resolved)
	return attachError(err)
}

// within reports whether path (resolved) is dir or inside it.
func within(path, dir string) bool {
	if dir == "" {
		return false
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(filepath.Clean(dir), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
