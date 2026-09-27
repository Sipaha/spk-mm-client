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
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
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

// Error codes; the UI shows localized messages for them.
const (
	CodeTooLarge       = "too_large"            // over the server's MaxFileSize (or the caller's limit)
	CodeTooMany        = "too_many"             // MaxPerChannel reached
	CodeDisabled       = "attachments_disabled" // the server does not take files
	CodeNotAFile       = "not_a_file"           // not a regular file (a folder, gone, unreadable)
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
	FileID  string `json:"-"` // the server's file id once uploaded
}

// Limits are a server's attachment settings; MaxFileSize 0 means the
// server's config is not known yet — nothing is refused then, the server
// decides at upload.
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
	// OnChange is called after the attachments of a channel changed (added,
	// removed, state, progress); it must not block or call the Store
	// synchronously.
	OnChange      func(srv int64, ch string)
	Parallel      int           // uploads at a time per server; 0 → 2
	Stall         time.Duration // an upload sending no bytes this long fails; 0 → 60 s
	ProgressEvery time.Duration // shortest gap between progress reports of one upload; 0 → 250 ms

	sweepHook func() // tests: runs in the sweep goroutine before it reads Dir
}

type key struct {
	srv int64
	ch  string
}

type item struct {
	Attachment
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
// settings and the channel's count. The returned limits apply to the size.
func (s *Store) admit(srv int64, ch string) (Limits, error) {
	lim, err := s.o.Backend.Limits(srv)
	if err != nil {
		return Limits{}, err
	}
	if lim.MaxFileSize > 0 && !lim.Enabled {
		return Limits{}, fail(CodeDisabled, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return lim, s.roomLocked(srv, ch)
}

func (s *Store) roomLocked(srv int64, ch string) error {
	if s.closed {
		return fail(CodeInternal, errors.New("attachments are closed"))
	}
	if len(s.lists[key{srv, ch}]) >= MaxPerChannel {
		return fail(CodeTooMany, nil)
	}
	return nil
}

// AddPath attaches a file on disk (a drop, the file dialog, a copied file):
// only its path, size and mtime are kept.
func (s *Store) AddPath(srv int64, ch, path string) (Attachment, error) {
	lim, err := s.admit(srv, ch)
	if err != nil {
		return Attachment{}, err
	}
	if !filepath.IsAbs(path) {
		return Attachment{}, fail(CodeNotAFile, fmt.Errorf("not an absolute path: %q", path))
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Attachment{}, fail(CodeNotAFile, err)
	}
	if !fi.Mode().IsRegular() {
		return Attachment{}, fail(CodeNotAFile, fmt.Errorf("%s is not a regular file", fi.Mode().Type()))
	}
	if lim.MaxFileSize > 0 && fi.Size() > lim.MaxFileSize {
		return Attachment{}, fail(CodeTooLarge, nil)
	}
	name := fi.Name()
	mt := mimeOf(name)
	if mt == "" {
		mt = "application/octet-stream"
	}
	it := &item{Attachment: Attachment{ID: newID(), Name: name, Size: fi.Size(), Mime: mt, State: StateStaged,
		Server: srv, Channel: ch}, path: path, mtime: fi.ModTime()}
	return s.insert(it)
}

// insert lists a new attachment and queues its upload.
func (s *Store) insert(it *item) (Attachment, error) {
	s.mu.Lock()
	if err := s.roomLocked(it.Server, it.Channel); err != nil {
		s.mu.Unlock()
		return Attachment{}, err
	}
	s.items[it.ID] = it
	k := key{it.Server, it.Channel}
	s.lists[k] = append(s.lists[k], it)
	s.enqueueLocked(it)
	s.broadcastLocked()
	a := it.Attachment
	s.mu.Unlock()
	s.notify(it.Server, it.Channel)
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

func (s *Store) notify(srv int64, ch string) {
	if s.o.OnChange != nil {
		s.o.OnChange(srv, ch)
	}
}

// List gives the attachments of a channel in the order they were added
// (never nil).
func (s *Store) List(srv int64, ch string) []Attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.lists[key{srv, ch}]
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
	s.notify(it.Server, it.Channel)
	return nil
}

// unlistLocked forgets an attachment and abandons its upload; the caller
// deletes the spool after unlocking.
func (s *Store) unlistLocked(it *item) {
	delete(s.items, it.ID)
	k := key{it.Server, it.Channel}
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
	s.notify(it.Server, it.Channel)
	s.pump(it.Server)
	return nil
}

// Wait returns once every attachment in ids is uploaded (nil), one of them
// failed (an *Error with its code) or is gone (CodeNotFound), or ctx ends.
func (s *Store) Wait(ctx context.Context, ids []string) error {
	for {
		s.mu.Lock()
		done, err := true, error(nil)
		for _, id := range ids {
			it := s.items[id]
			switch {
			case it == nil:
				err = fail(CodeNotFound, fmt.Errorf("attachment %s", id))
			case it.State == StateFailed:
				err = fail(it.Error, fmt.Errorf("attachment %s", id))
			case it.State != StateUploaded:
				done = false
			}
			if err != nil {
				break
			}
		}
		changed := s.changed
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
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
	var back []*item
	var chans []string
	for _, it := range s.items {
		if it.Server == srv && it.State == StateUploading {
			s.abandonLocked(it)
			it.State, it.Sent = StateStaged, 0
			back = append(back, it)
			chans = append(chans, it.Channel)
		}
	}
	q.pending = append(back, q.pending...)
	if len(back) > 0 {
		s.broadcastLocked()
	}
	s.mu.Unlock()
	for _, ch := range chans {
		s.notify(srv, ch)
	}
}

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
	chans := map[string]bool{}
	for _, it := range gone {
		s.removeSpool(it.spool)
		chans[it.Channel] = true
	}
	for ch := range chans {
		s.notify(srv, ch)
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
	f, err := os.Open(path)
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
