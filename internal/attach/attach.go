// Package attach keeps the files a user attaches to a message before it is
// sent: per server and channel, in memory (like a draft), each uploaded to
// the server as soon as it is staged and the server is live.
//
// Bytes are never held whole in memory: a file picked from disk is kept as
// its path (re-checked by size and mtime before the upload, streamed from
// disk), and bytes that come without a file (a pasted picture, a browser
// upload) are spooled to Dir as attach-<id>, deleted when the attachment
// goes away. Spools of a previous run are swept in the background by New.
//
// Uploads run two at a time per server, only while the server is live
// (Backend.Uploader), and are never retried by themselves: a failed one
// waits for Retry.
package attach

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// State of an attachment.
type State string

// States of an attachment.
const (
	StateStaged    State = "staged"    // waiting for the server to be live
	StateUploading State = "uploading" // being sent
	StateUploaded  State = "uploaded"  // on the server: FileID is set
	StateFailed    State = "failed"    // Error says why; Retry sends it again
)

// MaxPerChannel is the webapp's MAX_UPLOAD_FILES (a post's file_ids hold
// about ten ids).
const MaxPerChannel = 10

// DefaultMaxFileSize bounds sizes while a server's config is not known
// (the server's own default MaxFileSize).
const DefaultMaxFileSize = 100 << 20

// Error codes; the UI shows localized messages for them.
const (
	CodeTooLarge       = "too_large"            // over the server's MaxFileSize (or the caller's limit)
	CodeTooMany        = "too_many"             // MaxPerChannel reached
	CodeDisabled       = "attachments_disabled" // the server does not take files
	CodeNotAFile       = "not_a_file"           // not a regular file (a folder, gone, unreadable)
	CodeEmptyFile      = "empty_file"           // 0 bytes: the server refuses Content-Length 0
	CodeChanged        = "file_changed"         // the file changed or vanished after it was attached
	CodeNotFound       = "not_found"            // no such attachment
	CodeSessionExpired = "session_expired"      // 401 on upload
	CodeForbidden      = "forbidden"            // 403 on upload
	CodeUnreachable    = "unreachable"          // network error, or no bytes sent for Stall
	CodeInternal       = "internal"
)

// Error is what the store returns: a code for the UI and the cause.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "attach: " + e.Code
	}
	return "attach: " + e.Code + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code string, err error) *Error { return &Error{Code: code, Err: err} }

// Attachment is a snapshot of one attachment; the JSON part is what the UI
// gets (attachments_changed, Attachments).
type Attachment struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mime  string `json:"mime"`
	State State  `json:"state"`
	Sent  int64  `json:"sent"`  // bytes uploaded so far (Size once uploaded)
	Error string `json:"error"` // code of a failed upload

	Server  int64  `json:"-"`
	Channel string `json:"-"`
	Root    string `json:"-"` // "" — the channel's composer; else a thread's reply (Task 4)
	FileID  string `json:"-"` // the server's file id once uploaded
	Taken   bool   `json:"-"` // moved to a post being sent (Take): no longer in the composer
}

// Limits are a server's attachment settings; MaxFileSize 0 means the
// server's config is not known yet — DefaultMaxFileSize is enforced until
// it is (see admit), the server still makes the final call at upload.
type Limits struct {
	Enabled     bool
	MaxFileSize int64
}

// Backend connects the store to the servers (api.Service over the sync
// workers). Its methods must not call back into the Store.
type Backend interface {
	// Limits of a signed-in server; an error (passed through to the
	// caller) otherwise.
	Limits(srv int64) (Limits, error)
	// Uploader of a live server; nil while it is not live.
	Uploader(srv int64) Uploader
}

type Options struct {
	Dir     string // spools; created when first needed
	Backend Backend
	// OnChange is called after the attachments of (srv, ch, root) changed
	// (added, removed, state, progress); it must not block or call the
	// Store synchronously. root: "" the channel's composer, else a
	// thread's reply — see AGENTS.md "Вложения".
	OnChange      func(srv int64, ch, root string)
	Parallel      int           // uploads at a time per server; 0 → 2
	Stall         time.Duration // an upload sending no bytes this long fails; 0 → 60 s
	ProgressEvery time.Duration // shortest gap between progress reports of one upload; 0 → 250 ms

	sweepHook  func() // tests: runs in the sweep goroutine before it reads Dir
	unknownMax int64  // tests: the size bound of an unknown config; 0 → DefaultMaxFileSize
}

// key is a composer: the channel's ("" root) or one reply's thread.
type key struct {
	srv  int64
	ch   string
	root string
}

type item struct {
	Attachment
	seq   uint64 // order added (across channels)
	path  string // what is uploaded: the user's file or the spool
	spool string // spool file name in Dir, "" for a user's file
	mtime time.Time
	gen   int                     // bumped when a running upload is abandoned (removed, paused)
	stop  context.CancelCauseFunc // of the running upload
	shown time.Time               // last progress report
}

type queue struct {
	pending []*item
	running int
	paused  bool // the worker stopped: nothing starts until Wake
	// epoch is bumped by every Pause: a pump that asked the backend for an
	// uploader before it (a worker being stopped) must not use it.
	epoch int
}

// Store holds the attachments of every server.
type Store struct {
	o      Options
	life   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	items   map[string]*item
	lists   map[key][]*item
	queues  map[int64]*queue
	spools  map[string]bool // spool names in use (attachments and bodies being spooled)
	seq     uint64          // last item.seq
	changed chan struct{}   // closed and replaced on every state change (Wait)
}

// New makes a store and sweeps spools of a previous run in the background:
// it never touches the disk itself.
func New(o Options) *Store {
	if o.Parallel <= 0 {
		o.Parallel = 2
	}
	if o.Stall <= 0 {
		o.Stall = 60 * time.Second
	}
	if o.ProgressEvery <= 0 {
		o.ProgressEvery = 250 * time.Millisecond
	}
	if o.unknownMax <= 0 {
		o.unknownMax = DefaultMaxFileSize
	}
	s := &Store{o: o, items: map[string]*item{}, lists: map[key][]*item{}, queues: map[int64]*queue{},
		spools: map[string]bool{}, changed: make(chan struct{})}
	s.life, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.sweep()
	}()
	return s
}

const spoolPrefix = "attach-"

// sweep deletes spools left by a previous run (the app quit or crashed
// before they were sent); those of this run are kept.
func (s *Store) sweep() {
	if s.o.sweepHook != nil {
		s.o.sweepHook()
	}
	des, err := os.ReadDir(s.o.Dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("attachment spool sweep failed", "err", err)
		}
		return
	}
	for _, de := range des {
		name := de.Name()
		if !strings.HasPrefix(name, spoolPrefix) || s.life.Err() != nil {
			continue
		}
		s.mu.Lock()
		live := s.spools[name]
		s.mu.Unlock()
		// A name in use now is never an old one: ids are random.
		if !live {
			if err := os.Remove(filepath.Join(s.o.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("could not remove an old attachment spool", "name", name, "err", err)
			}
		}
	}
}

var idEncoding = base32.NewEncoding("ybndrfg8ejkmcpqxot1uwisza345h769").WithPadding(base32.NoPadding)

// newID is a Mattermost-style id (26 characters of [a-z0-9]): it goes to
// the server as client_id and into /media/<srv>/staged/<id>.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return idEncoding.EncodeToString(b[:])
}

// mimeOf gives a media type without parameters by the name's extension, ""
// when unknown.
func mimeOf(name string) string {
	t := mime.TypeByExtension(filepath.Ext(name))
	if mt, _, err := mime.ParseMediaType(t); err == nil {
		return mt
	}
	return ""
}

// admit checks what can be checked before anything is read: the server's
// settings and the channel's count. It returns the size bound: the
// server's MaxFileSize, or DefaultMaxFileSize while its config is unknown
// (Enabled is then not held against it — the server decides).
func (s *Store) admit(srv int64, ch, root string) (int64, error) {
	lim, err := s.o.Backend.Limits(srv)
	if err != nil {
		return 0, err
	}
	maxSize := lim.MaxFileSize
	if maxSize <= 0 {
		maxSize = s.o.unknownMax
	} else if !lim.Enabled {
		return 0, fail(CodeDisabled, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return maxSize, s.roomLocked(srv, ch, root)
}

func (s *Store) roomLocked(srv int64, ch, root string) error {
	if s.closed {
		return fail(CodeInternal, errors.New("attachments are closed"))
	}
	if len(s.lists[key{srv, ch, root}]) >= MaxPerChannel {
		return fail(CodeTooMany, nil)
	}
	return nil
}

// AddPath attaches a file on disk (a drop, the file dialog, a copied file)
// to (srv, ch, root)'s composer: only its path, size and mtime are kept.
func (s *Store) AddPath(srv int64, ch, root, path string) (Attachment, error) {
	maxSize, err := s.admit(srv, ch, root)
	if err != nil {
		return Attachment{}, err
	}
	if !filepath.IsAbs(path) {
		return Attachment{}, fail(CodeNotAFile, fmt.Errorf("not an absolute path: %q", path))
	}
	f, fi, err := openRegular(path)
	if err != nil {
		return Attachment{}, fail(CodeNotAFile, err)
	}
	defer f.Close()
	switch {
	case fi.Size() == 0:
		return Attachment{}, fail(CodeEmptyFile, nil)
	case fi.Size() > maxSize:
		return Attachment{}, fail(CodeTooLarge, nil)
	}
	mt := mimeOf(path)
	if mt == "" {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		mt = mediaType(http.DetectContentType(head[:n]))
	}
	it := &item{Attachment: Attachment{ID: newID(), Name: cleanName(filepath.Base(path)), Size: fi.Size(), Mime: mt,
		State: StateStaged, Server: srv, Channel: ch, Root: root}, path: path, mtime: fi.ModTime()}
	return s.insert(it)
}

// openRegular opens a regular file only: the type is checked before the
// open (a FIFO would block it) and again on the open file.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file", fi.Mode().Type())
	}
	f, err := os.OpenFile(path, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	fi, err = f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", fi.Mode().Type())
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// insert lists a new attachment and queues its upload.
func (s *Store) insert(it *item) (Attachment, error) {
	s.mu.Lock()
	if err := s.roomLocked(it.Server, it.Channel, it.Root); err != nil {
		s.mu.Unlock()
		return Attachment{}, err
	}
	s.seq++
	it.seq = s.seq
	s.items[it.ID] = it
	k := key{it.Server, it.Channel, it.Root}
	s.lists[k] = append(s.lists[k], it)
	s.enqueueLocked(it)
	s.broadcastLocked()
	a := it.Attachment
	s.mu.Unlock()
	s.notify(it.Server, it.Channel, it.Root)
	s.pump(it.Server)
	return a, nil
}

func (s *Store) queue(srv int64) *queue {
	q := s.queues[srv]
	if q == nil {
		q = &queue{}
		s.queues[srv] = q
	}
	return q
}

func (s *Store) enqueueLocked(it *item) {
	q := s.queue(it.Server)
	q.pending = append(q.pending, it)
}

// broadcastLocked wakes Wait callers.
func (s *Store) broadcastLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *Store) notify(srv int64, ch, root string) {
	if s.o.OnChange != nil {
		s.o.OnChange(srv, ch, root)
	}
}

// List gives the attachments of (srv, ch, root)'s composer in the order
// they were added (never nil).
func (s *Store) List(srv int64, ch, root string) []Attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.lists[key{srv, ch, root}]
	out := make([]Attachment, 0, len(l))
	for _, it := range l {
		out = append(out, it.Attachment)
	}
	return out
}

// Get gives one attachment.
func (s *Store) Get(id string) (Attachment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.items[id]
	if it == nil {
		return Attachment{}, false
	}
	return it.Attachment, true
}

// Remove drops an attachment: its upload is cancelled, its spool deleted.
// A file already uploaded stays on the server unattached (harmless; the
// webapp leaves them too).
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	it := s.items[id]
	if it == nil {
		s.mu.Unlock()
		return fail(CodeNotFound, nil)
	}
	s.unlistLocked(it)
	s.broadcastLocked()
	s.mu.Unlock()
	s.removeSpool(it.spool)
	s.notify(it.Server, it.Channel, it.Root)
	return nil
}

// ReleaseComposer drops every attachment of (srv, ch, root)'s composer,
// including one still uploading: its composer itself is gone (the channel
// was left, taking its held threads with it —
// state.Server.TakeForgottenComposers), so nothing can ever send them, and
// leaving an uploading one listed would let it reappear (still
// StateUploading, stuck forever — its own upload is cancelled by
// unlistLocked, but nothing ever retries a listed item stuck mid-upload) if
// the user rejoins the channel or reopens the thread before it settles.
// unlistLocked cancels a running upload the same way Remove does; it
// either never reaches the server or finishes into an orphaned file there
// (harmless, same as Remove leaves one).
func (s *Store) ReleaseComposer(srv int64, ch, root string) {
	s.mu.Lock()
	gone := append([]*item(nil), s.lists[key{srv, ch, root}]...)
	for _, it := range gone {
		s.unlistLocked(it)
	}
	if len(gone) > 0 {
		s.broadcastLocked()
	}
	s.mu.Unlock()
	for _, it := range gone {
		s.removeSpool(it.spool)
	}
	if len(gone) > 0 {
		s.notify(srv, ch, root)
	}
}

// unlistLocked forgets an attachment and abandons its upload; the caller
// deletes the spool after unlocking.
func (s *Store) unlistLocked(it *item) {
	delete(s.items, it.ID)
	k := key{it.Server, it.Channel, it.Root}
	l := s.lists[k]
	for i, x := range l {
		if x == it {
			l = append(l[:i:i], l[i+1:]...)
			break
		}
	}
	if len(l) == 0 {
		delete(s.lists, k)
	} else {
		s.lists[k] = l
	}
	s.abandonLocked(it)
	// A queued one is skipped by pump: it is no longer in items.
}

// abandonLocked cancels a running upload; its goroutine sees a new gen and
// drops its result.
func (s *Store) abandonLocked(it *item) {
	it.gen++
	if it.stop != nil {
		it.stop(errAbandoned)
		it.stop = nil
	}
}

var errAbandoned = errors.New("attach: upload abandoned")

func (s *Store) removeSpool(name string) {
	if name == "" {
		return
	}
	if err := os.Remove(filepath.Join(s.o.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Left for the next start's sweep.
		slog.Warn("could not remove an attachment spool", "name", name, "err", err)
	}
	s.mu.Lock()
	delete(s.spools, name)
	s.mu.Unlock()
}

// Retry sends a failed attachment again (only when the server is live);
// other states are left alone.
func (s *Store) Retry(id string) error {
	s.mu.Lock()
	it := s.items[id]
	if it == nil {
		s.mu.Unlock()
		return fail(CodeNotFound, nil)
	}
	if it.State != StateFailed {
		s.mu.Unlock()
		return nil
	}
	it.State, it.Error, it.Sent = StateStaged, "", 0
	s.enqueueLocked(it)
	s.broadcastLocked()
	s.mu.Unlock()
	s.notify(it.Server, it.Channel, it.Root)
	s.pump(it.Server)
	return nil
}

// Take moves attachments of (srv, ch, root)'s composer to a post being
// sent: they are no longer listed (nor counted against MaxPerChannel), but
// stay alive — uploads go on, Wait, Retry and Remove work — until the post
// removes them. All or nothing: an id that is not in that composer's list
// (unknown, another server's, channel's or root's, taken already) fails
// the whole call with CodeNotFound — a thread's attachments can never be
// sent as the channel's (or the reverse). A repeated id is taken once; the
// result keeps the order given.
func (s *Store) Take(srv int64, ch, root string, ids []string) ([]Attachment, error) {
	s.mu.Lock()
	k := key{srv, ch, root}
	var take []*item
	for _, id := range ids {
		it := s.items[id]
		if it == nil || it.Taken || it.Server != srv || it.Channel != ch || it.Root != root {
			s.mu.Unlock()
			return nil, fail(CodeNotFound, fmt.Errorf("attachment %s", id))
		}
		if !slices.Contains(take, it) {
			take = append(take, it)
		}
	}
	out := make([]Attachment, 0, len(take))
	for _, it := range take {
		it.Taken = true
		out = append(out, it.Attachment)
	}
	l := slices.DeleteFunc(s.lists[k], func(it *item) bool { return it.Taken })
	if len(l) == 0 {
		delete(s.lists, k)
	} else {
		s.lists[k] = l
	}
	s.mu.Unlock()
	if len(take) > 0 {
		s.notify(srv, ch, root)
	}
	return out, nil
}

// Wait returns the server file ids of the attachments in ids (in that
// order) once every one is uploaded; an *Error with its code once one of
// them failed, CodeNotFound once one is gone; or ctx's error.
func (s *Store) Wait(ctx context.Context, ids []string) ([]string, error) {
	for {
		s.mu.Lock()
		fileIDs, err := make([]string, 0, len(ids)), error(nil)
		for _, id := range ids {
			it := s.items[id]
			switch {
			case it == nil:
				err = fail(CodeNotFound, fmt.Errorf("attachment %s", id))
			case it.State == StateFailed:
				err = fail(it.Error, fmt.Errorf("attachment %s", id))
			case it.State == StateUploaded:
				fileIDs = append(fileIDs, it.FileID)
			}
			if err != nil {
				break
			}
		}
		changed := s.changed
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if len(fileIDs) == len(ids) {
			return fileIDs, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Wake starts queued uploads of a server that became live.
func (s *Store) Wake(srv int64) {
	s.mu.Lock()
	if q := s.queues[srv]; q != nil {
		q.paused = false
	}
	s.mu.Unlock()
	s.pump(srv)
}

// Pause puts a server's running uploads back into its queue and holds the
// queue until Wake: its worker is being stopped (sign-in again, sign-out),
// and its token may be revoked right after.
func (s *Store) Pause(srv int64) {
	s.mu.Lock()
	q := s.queue(srv)
	q.paused = true
	q.epoch++
	var back []*item
	chans := map[chanRoot]bool{}
	for _, it := range s.items {
		if it.Server == srv && it.State == StateUploading {
			s.abandonLocked(it)
			it.State, it.Sent = StateStaged, 0
			back = append(back, it)
			chans[chanRoot{it.Channel, it.Root}] = true
		}
	}
	slices.SortFunc(back, func(a, b *item) int { return cmp.Compare(a.seq, b.seq) })
	q.pending = append(back, q.pending...)
	if len(back) > 0 {
		s.broadcastLocked()
	}
	s.mu.Unlock()
	for cr := range chans {
		s.notify(srv, cr.ch, cr.root)
	}
}

// chanRoot is a composer's (channel, root) half of key, used where the
// server is already fixed (Pause, DropServer notify sets).
type chanRoot struct{ ch, root string }

// DropServer forgets every attachment of a server (signed out, removed).
func (s *Store) DropServer(srv int64) {
	s.mu.Lock()
	var gone []*item
	for _, it := range s.items {
		if it.Server == srv {
			gone = append(gone, it)
		}
	}
	for _, it := range gone {
		s.unlistLocked(it)
	}
	delete(s.queues, srv)
	if len(gone) > 0 {
		s.broadcastLocked()
	}
	s.mu.Unlock()
	chans := map[chanRoot]bool{}
	for _, it := range gone {
		s.removeSpool(it.spool)
		chans[chanRoot{it.Channel, it.Root}] = true
	}
	for cr := range chans {
		s.notify(srv, cr.ch, cr.root)
	}
}

// Open opens the file of a server's attachment (the staged preview).
func (s *Store) Open(srv int64, id string) (*os.File, Attachment, error) {
	s.mu.Lock()
	it := s.items[id]
	if it == nil || it.Server != srv {
		s.mu.Unlock()
		return nil, Attachment{}, fail(CodeNotFound, nil)
	}
	path, a := it.path, it.Attachment
	s.mu.Unlock()
	f, _, err := openRegular(path)
	if err != nil {
		return nil, Attachment{}, fail(CodeNotFound, err)
	}
	return f, a, nil
}

// Close cancels every upload, waits for them (and the sweep) and deletes
// every spool; later calls are refused.
func (s *Store) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	var names []string
	for _, it := range s.items {
		if it.spool != "" {
			names = append(names, it.spool)
		}
	}
	s.items, s.lists, s.queues = map[string]*item{}, map[key][]*item{}, map[int64]*queue{}
	s.broadcastLocked()
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	for _, n := range names {
		s.removeSpool(n)
	}
}
