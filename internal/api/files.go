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

// inert are the only types "Open" hands to the system: data formats whose
// usual handlers display rather than run them. Everything else — scripts,
// launchers, installers, disk images, HTML/SVG, macro-enabled or legacy
// office files, and names without an extension (the desktop would sniff the
// content) — is saved but not opened: the file came from someone else, and
// the opener is ShellExecute on Windows, open(1) on macOS, xdg-open
// elsewhere.
var inert = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".tif": true, ".tiff": true,
	".heic": true, ".avif": true,
	".pdf": true,
	".txt": true, ".log": true, ".csv": true, ".tsv": true, ".json": true, ".md": true, ".yaml": true, ".yml": true,
	".docx": true, ".xlsx": true, ".pptx": true, ".odt": true, ".ods": true, ".odp": true, ".rtf": true,
	".mp3": true, ".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".flac": true, ".m4a": true,
	".mp4": true, ".webm": true, ".mkv": true, ".mov": true, ".avi": true,
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
}

// openable checks the final saved path, case-insensitively.
func openable(path string) bool { return inert[strings.ToLower(filepath.Ext(path))] }

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
	key := fmt.Sprintf("%d/%s", id, fileID)
	path, err := s.shared(ctx, key, func() (string, error) {
		return s.fetchFile(ctx, w.REST().WithHTTPClient(s.transfer), id, fileID, key)
	})
	if err != nil {
		return SavedFile{}, err
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

// download is a save of one file in progress; callers asking for the same
// file meanwhile wait for it instead of saving a second copy.
type download struct {
	done chan struct{}
	path string
	err  error
}

// shared runs fetch for key unless a save of key is already running, in
// which case it waits for that one's result (each caller then opens the
// file or not as it asked). A waiter whose ctx ends stops waiting; the save
// goes on for the others.
func (s *Service) shared(ctx context.Context, key string, fetch func() (string, error)) (string, error) {
	s.filesMu.Lock()
	d := s.saving[key]
	if d == nil {
		d = &download{done: make(chan struct{})}
		s.saving[key] = d
		s.filesMu.Unlock()
		d.path, d.err = fetch()
		s.filesMu.Lock()
		delete(s.saving, key)
		s.filesMu.Unlock()
		close(d.done)
		return d.path, d.err
	}
	s.filesMu.Unlock()
	select {
	case <-d.done:
		return d.path, d.err
	case <-ctx.Done():
		return "", actionError(ctx.Err())
	}
}

// fetchFile saves the file into the downloads directory, or finds the copy
// saved earlier this session.
func (s *Service) fetchFile(ctx context.Context, rc *rest.Client, id int64, fileID, key string) (string, error) {
	ictx, cancel := s.bounded(ctx)
	info, err := rc.FileInfo(ictx, fileID)
	cancel()
	if err != nil {
		return "", fileError(err)
	}
	dir, err := downloadsDir(s.getenv)
	if err != nil {
		return "", coded(CodeInternal, err)
	}
	if path, ok := s.savedPath(key, info.Size); ok {
		return path, nil
	}
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	path, err := saveInto(dctx, rc, dir, fileID, info)
	if err != nil {
		return "", fileError(err)
	}
	s.remember(key, path)
	slog.Info("file saved", "srv", id, "file", fileID, "path", path)
	return path, nil
}

// maxSaved bounds the session's record of saved files.
const maxSaved = 512

func (s *Service) remember(key, path string) {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	if _, ok := s.saved[key]; !ok && len(s.saved) >= maxSaved {
		for k := range s.saved { // drop an arbitrary entry: it only costs a re-download
			delete(s.saved, k)
			break
		}
	}
	s.saved[key] = path
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

// linkFile and renameFile are os.Link and os.Rename (tests: a file system
// without hard links).
var (
	linkFile   = os.Link
	renameFile = os.Rename
)

var errSizeMismatch = errors.New("file size differs from its info")

// saveInto streams the file into a hidden ".<name>.part" in dir and, once
// complete and of the declared size, links it to a free final name: a
// partial file never appears under the final name, and nothing is replaced.
func saveInto(ctx context.Context, rc *rest.Client, dir, fileID string, info model.FileInfo) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	resp, err := rc.Stream(ctx, "/api/v4/files/"+url.PathEscape(fileID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	name := sanitizeName(info.Name)
	f, tmp, err := createExcl(dir, "."+name, ".part")
	if err != nil {
		return "", err
	}
	// A failed download removes its .part. A published one does not: publish
	// removed or renamed it, and after a rename the name may already be
	// another download's .part.
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tmp)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, info.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != info.Size {
		err = fmt.Errorf("%w: got %d bytes, want %d", errSizeMismatch, n, info.Size)
	}
	if err != nil {
		return "", err
	}
	path, err := publish(tmp, dir, name)
	published = err == nil
	return path, err
}

// uniqueNames yields name, "stem (1).ext", … "stem (999).ext".
func uniqueNames(name string, yield func(string) bool) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		if !yield(n) {
			return
		}
	}
}

var errNoFreeName = errors.New("no free file name")

// createExcl creates prefix+suffix in dir, or prefix+" (N)"+suffix if
// taken; O_EXCL makes the choice race-free and never follows a symlink.
func createExcl(dir, prefix, suffix string) (f *os.File, path string, err error) {
	err = errNoFreeName
	uniqueNames(prefix+suffix, func(n string) bool {
		p := filepath.Join(dir, n)
		var e error
		if f, e = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); e == nil {
			path, err = p, nil
			return false
		}
		if !errors.Is(e, fs.ErrExist) {
			err = e
			return false
		}
		return true
	})
	return f, path, err
}

// publish gives the finished tmp a free name in dir: os.Link fails if the
// name exists (even as a dangling symlink), so nothing is replaced and two
// downloads never race for one name. Where hard links are not supported
// (FAT, some network mounts), an O_EXCL placeholder reserves the name and
// the rename replaces only that placeholder. On success tmp is gone.
func publish(tmp, dir, name string) (string, error) {
	path, err := "", errNoFreeName
	uniqueNames(name, func(n string) bool {
		p := filepath.Join(dir, n)
		e := linkFile(tmp, p)
		switch {
		case e == nil:
			_ = os.Remove(tmp) // still ours: the link holds the file
			path, err = p, nil
			return false
		case errors.Is(e, fs.ErrExist):
			return true
		}
		f, fp, ce := createExcl(dir, strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name))
		if ce != nil {
			err = ce
			return false
		}
		_ = f.Close()
		if re := renameFile(tmp, fp); re != nil {
			_ = os.Remove(fp)
			err = re
			return false
		}
		path, err = fp, nil
		return false
	})
	return path, err
}

// windowsReserved are device names Windows refuses (or worse, opens as a
// device) whatever the extension.
var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// hidden are characters that make a name read differently from what it is:
// bidi overrides (which disguise an extension) and zero-width/format marks.
func hidden(r rune) bool {
	return unicode.Is(unicode.Bidi_Control, r) || (r >= 0x200B && r <= 0x200F) || r == 0x2060 || r == 0xFEFF
}

// sanitizeName keeps a server-supplied file name inside the downloads
// directory and valid on every OS: the last path element only, no control,
// hidden or reserved characters, valid UTF-8, not empty or dots only, not a
// Windows device name, no trailing dot or space, at most 200 bytes.
func sanitizeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || hidden(r) || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	stem, _, _ := strings.Cut(name, ".")
	if windowsReserved[strings.ToUpper(strings.TrimSpace(stem))] {
		name = "_" + name
	}
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "file"
	}
	return name
}
