package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adrg/xdg"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

// SavedFile is where a downloaded file landed and whether it was handed to
// the system to open.
type SavedFile struct {
	Path   string `json:"path"`
	Opened bool   `json:"opened"`
}

const downloadTimeout = 30 * time.Minute

var fileIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// launchers are never handed to the system by "Open": opening them runs
// code (or asks the desktop to), and the file came from someone else. They
// are still saved.
var launchers = map[string]bool{
	".desktop": true, ".sh": true, ".bash": true, ".zsh": true, ".run": true, ".bin": true, ".appimage": true,
	".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".ps1": true, ".vbs": true,
	".jar": true, ".py": true, ".pl": true, ".rb": true, ".deb": true, ".rpm": true, ".apk": true, ".app": true,
	".command": true, ".lnk": true, ".reg": true,
	".flatpakref": true, ".flatpakrepo": true, ".snap": true, ".pyw": true, ".pyz": true, ".hta": true,
	".js": true, ".jse": true, ".vbe": true, ".wsf": true, ".pif": true, ".cpl": true, ".msp": true,
}

// openable: not a launcher and has an extension — without one the desktop
// picks the handler by sniffing the content, so a script or binary could
// reach an executing handler under an innocent name.
func openable(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext != "" && ext != "." && !launchers[ext]
}

// RecordingOpener remembers opened files (browser mode, tests): there is no
// system application to hand them to.
type RecordingOpener struct {
	mu    sync.Mutex
	paths []string
}

func (r *RecordingOpener) Open(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	return nil
}

func (r *RecordingOpener) List() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.paths...)
}

// SetFileOpener sets how "Open" hands a saved file to the system; nil: it
// only saves.
func (s *Service) SetFileOpener(fn Opener) {
	s.mu.Lock()
	s.fileOpen = fn
	s.mu.Unlock()
}

// DownloadFile saves a file of a post into the downloads directory.
func (s *Service) DownloadFile(ctx context.Context, id int64, fileID string) (SavedFile, error) {
	return s.saveFile(ctx, id, fileID, false)
}

// OpenFile saves a file like DownloadFile and opens it with the system
// application, unless it is a launcher.
func (s *Service) OpenFile(ctx context.Context, id int64, fileID string) (SavedFile, error) {
	return s.saveFile(ctx, id, fileID, true)
}

func (s *Service) saveFile(ctx context.Context, id int64, fileID string, open bool) (SavedFile, error) {
	if !fileIDRe.MatchString(fileID) {
		return SavedFile{}, coded(CodeNoFile, nil)
	}
	w, err := s.writer(ctx, id)
	if err != nil {
		return SavedFile{}, err
	}
	rc := w.REST().WithHTTPClient(s.transfer)
	ictx, cancel := s.bounded(ctx)
	info, err := rc.FileInfo(ictx, fileID)
	cancel()
	if err != nil {
		return SavedFile{}, fileError(err)
	}
	dir, err := downloadsDir(s.getenv)
	if err != nil {
		return SavedFile{}, coded(CodeInternal, err)
	}
	key := fmt.Sprintf("%d/%s", id, fileID)
	path, ok := s.savedPath(key, info.Size)
	if !ok {
		dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
		defer cancel()
		if path, err = saveInto(dctx, rc, dir, info); err != nil {
			return SavedFile{}, fileError(err)
		}
		s.filesMu.Lock()
		s.saved[key] = path
		s.filesMu.Unlock()
		slog.Info("file saved", "srv", id, "file", fileID, "path", path)
	}
	res := SavedFile{Path: path}
	s.mu.Lock()
	openFn := s.fileOpen
	s.mu.Unlock()
	if open && openFn != nil && openable(path) {
		if err := openFn(path); err != nil {
			slog.Warn("could not open file", "path", path, "err", err)
		} else {
			res.Opened = true
		}
	}
	return res, nil
}

// savedPath: the file saved earlier this session, if it is still there
// unchanged (same size).
func (s *Service) savedPath(key string, size int64) (string, bool) {
	s.filesMu.Lock()
	p, ok := s.saved[key]
	s.filesMu.Unlock()
	if !ok {
		return "", false
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() != size {
		return "", false
	}
	return p, true
}

func fileError(err error) error {
	var re *rest.Error
	if errors.As(err, &re) && (re.Status == http.StatusNotFound || re.Status == http.StatusBadRequest) {
		return coded(CodeNoFile, err)
	}
	return actionError(err)
}

// downloadsDir: SPK_MM_CLIENT_DOWNLOADS (tests, e2e), else the XDG
// download directory (localized, e.g. ~/Загрузки), else ~/Downloads. An XDG
// entry pointing at the home directory itself ("$HOME/" disables it in
// user-dirs.dirs) is not used: a server-named dotfile such as .profile
// must never land there.
func downloadsDir(getenv func(string) string) (string, error) {
	if d := getenv("SPK_MM_CLIENT_DOWNLOADS"); d != "" {
		return d, nil
	}
	home, herr := os.UserHomeDir()
	if d := xdg.UserDirs.Download; d != "" && (herr != nil || filepath.Clean(d) != filepath.Clean(home)) {
		return d, nil
	}
	if herr != nil {
		return "", herr
	}
	return filepath.Join(home, "Downloads"), nil
}

func saveInto(ctx context.Context, rc *rest.Client, dir string, info model.FileInfo) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	resp, err := rc.Stream(ctx, "/api/v4/files/"+url.PathEscape(info.ID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, path, err := createUnique(dir, sanitizeName(info.Name))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// createUnique creates name in dir, or "stem (N).ext" if taken; O_EXCL
// makes the choice race-free (and never follows a symlink planted there).
func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, n)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("no free file name")
}

// sanitizeName keeps a server-supplied file name inside the downloads
// directory and valid on every OS: the last path element only, no control,
// bidi-override (which disguise an extension) or reserved characters, valid
// UTF-8, not empty or dots only, at most 200 bytes.
func sanitizeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if strings.Trim(name, ".") == "" {
		return "file"
	}
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}
