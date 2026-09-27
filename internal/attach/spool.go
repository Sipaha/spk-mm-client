package attach

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AddBytes attaches bytes that come without a file on disk (a pasted
// picture, a browser upload): they are streamed into a spool, refusing more
// than limit bytes (too_large; limit ≤ 0: the server's MaxFileSize only —
// DefaultMaxFileSize while unknown; the smaller of the two otherwise) or
// zero bytes (empty_file). name is cleaned (cleanName); mime, when empty,
// comes from the name or the content.
func (s *Store) AddBytes(srv int64, ch, name, mimeType string, r io.Reader, limit int64) (Attachment, error) {
	maxSize, err := s.admit(srv, ch)
	if err != nil {
		return Attachment{}, err
	}
	if limit <= 0 || maxSize < limit {
		limit = maxSize
	}
	id := newID()
	spool := spoolPrefix + id
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Attachment{}, fail(CodeInternal, errors.New("attachments are closed"))
	}
	s.spools[spool] = true // before the file exists: the sweep must not take it
	s.mu.Unlock()
	it, err := s.spool(spool, r, limit)
	if err != nil {
		s.removeSpool(spool)
		return Attachment{}, err
	}
	it.ID, it.Server, it.Channel, it.State = id, srv, ch, StateStaged
	it.Name = cleanName(name)
	it.Mime = mediaType(mimeType)
	if it.Mime == "" {
		it.Mime = mimeOf(it.Name)
	}
	if it.Mime == "" {
		it.Mime = it.sniffed
	}
	a, err := s.insert(&it.item)
	if err != nil {
		s.removeSpool(spool)
	}
	return a, err
}

type spooled struct {
	item
	sniffed string
}

// spool writes r into Dir/name (owner-only), refusing more than limit bytes.
func (s *Store) spool(name string, r io.Reader, limit int64) (*spooled, error) {
	if err := os.MkdirAll(s.o.Dir, 0o700); err != nil {
		return nil, fail(CodeInternal, err)
	}
	path := filepath.Join(s.o.Dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fail(CodeInternal, err)
	}
	defer f.Close()
	br := bufio.NewReaderSize(r, 512)
	head, _ := br.Peek(512)
	sniffed := mediaType(http.DetectContentType(head))
	n, err := io.Copy(f, io.LimitReader(br, limit+1))
	switch {
	case err != nil:
		return nil, fail(CodeInternal, err)
	case n > limit:
		return nil, fail(CodeTooLarge, nil)
	case n == 0:
		return nil, fail(CodeEmptyFile, nil)
	}
	if err := f.Close(); err != nil {
		return nil, fail(CodeInternal, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fail(CodeInternal, err)
	}
	return &spooled{item: item{Attachment: Attachment{Size: n}, path: path, spool: name, mtime: fi.ModTime()},
		sniffed: sniffed}, nil
}

// maxName bounds an attachment's name, in runes.
const maxName = 200

// cleanName makes a file name from outside (the page, a file on disk)
// safe to show and send: its last path element, without control and
// format characters (bidi overrides, zero-width marks — a name must read
// as what it is), at most maxName runes (a short extension kept);
// "attachment" when nothing is left.
func cleanName(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	var b strings.Builder
	for _, r := range name {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	name = strings.TrimSpace(b.String())
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	if utf8.RuneCountInString(name) <= maxName {
		return name
	}
	ext := filepath.Ext(name)
	if utf8.RuneCountInString(ext) > 16 {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	return string(stem[:maxName-utf8.RuneCountInString(ext)]) + ext
}

// mediaType drops parameters ("text/plain; charset=utf-8" → "text/plain").
func mediaType(t string) string {
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return strings.ToLower(strings.TrimSpace(t))
}
