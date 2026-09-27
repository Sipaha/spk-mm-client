//go:build wails && gtk3

package desktop

/*
#cgo linux pkg-config: gtk+-3.0
#include <stdint.h>
#include <string.h>
#include <gtk/gtk.h>

// Answers, in clipboard_gtk_cb.go (//export: its preamble may only declare).
extern void spkClipTargets(uintptr_t h, char **names, int n);
extern void spkClipContents(uintptr_t h, void *data, int n);
extern void spkClipImage(uintptr_t h, void *pixbuf);

static void spk_targets_cb(GtkClipboard *c, GdkAtom *atoms, gint n, gpointer h) {
	char **names = NULL;
	if (atoms == NULL || n < 0) n = 0;
	if (n > 0) {
		names = g_new0(char *, n);
		for (gint i = 0; i < n; i++) names[i] = gdk_atom_name(atoms[i]);
	}
	spkClipTargets((uintptr_t)h, names, n);
	for (gint i = 0; i < n; i++) g_free(names[i]);
	g_free(names);
}

// The selection data lives only during the callback: copied into C memory
// the Go side frees (g_free) once read — the bytes never go through the Go
// heap.
static void spk_contents_cb(GtkClipboard *c, GtkSelectionData *sd, gpointer h) {
	gint n = sd ? gtk_selection_data_get_length(sd) : -1;
	void *copy = NULL;
	if (n > 0) {
		copy = g_malloc(n);
		memcpy(copy, gtk_selection_data_get_data(sd), n);
	}
	spkClipContents((uintptr_t)h, copy, n);
}

static void spk_image_cb(GtkClipboard *c, GdkPixbuf *pb, gpointer h) {
	if (pb) g_object_ref(pb);
	spkClipImage((uintptr_t)h, pb);
}

static GtkClipboard *spk_clipboard(void) { return gtk_clipboard_get(GDK_SELECTION_CLIPBOARD); }

static void spk_request_targets(uintptr_t h) {
	gtk_clipboard_request_targets(spk_clipboard(), spk_targets_cb, (gpointer)h);
}

static void spk_request_contents(uintptr_t h, const char *target) {
	gtk_clipboard_request_contents(spk_clipboard(), gdk_atom_intern(target, FALSE), spk_contents_cb, (gpointer)h);
}

static void spk_request_image(uintptr_t h) {
	gtk_clipboard_request_image(spk_clipboard(), spk_image_cb, (gpointer)h);
}

static int spk_pixbuf_png(void *pb, gchar **buf, gsize *size) {
	GError *err = NULL;
	gboolean ok = gdk_pixbuf_save_to_buffer((GdkPixbuf *)pb, buf, size, "png", &err, NULL);
	if (err) g_error_free(err);
	return ok;
}

static void spk_unref(void *o) { g_object_unref(o); }
*/
import "C"

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime/cgo"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mm-client/internal/api"
)

// gtkClipboard reads the GTK clipboard for api.Service.AttachFromClipboard.
// GTK may only be called on its main thread, so every request is posted
// there (application.InvokeAsync) and is asynchronous (request_*, not
// wait_*): the main loop keeps running, and the answer comes back to the
// asking goroutine, which gives up when ctx ends — a hung clipboard owner
// blocks neither the UI nor the caller. Callers are binding goroutines,
// never the main thread (waiting there would stop the loop that answers).
type gtkClipboard struct{}

func newClipboard() api.Clipboard { return gtkClipboard{} }

// pasteKeys are the paste keys observed natively (observe_gtk.go).
var pasteKeys = newPasteGate()

// TakePasteGesture: a Ctrl+V / Shift+Insert pressed in the window within
// pasteWindow, used up now.
func (gtkClipboard) TakePasteGesture() bool { return pasteKeys.take() }

var errNoClipboardData = errors.New("the clipboard has no such data")

// cData is selection data copied into C memory.
type cData struct {
	p unsafe.Pointer
	n int
}

func (d cData) free() { C.g_free(C.gpointer(d.p)) }

// cReader reads C memory without copying it into the Go heap; Close frees it.
type cReader struct {
	*bytes.Reader
	once sync.Once
	free func()
}

func (r *cReader) Close() error {
	r.once.Do(func() {
		r.Reset(nil)
		r.free()
	})
	return nil
}

func newCReader(p unsafe.Pointer, n int, free func()) io.ReadCloser {
	var b []byte
	if n > 0 {
		b = unsafe.Slice((*byte)(p), n)
	}
	return &cReader{Reader: bytes.NewReader(b), free: free}
}

// ask posts one request to the main thread and waits for its answer.
func ask[T any](ctx context.Context, p *pending[T], request func(C.uintptr_t)) (T, error) {
	h := cgo.NewHandle(p)
	application.InvokeAsync(func() { request(C.uintptr_t(h)) })
	return p.wait(ctx)
}

func (gtkClipboard) Targets(ctx context.Context) ([]string, error) {
	return ask(ctx, newPending[[]string](nil), func(h C.uintptr_t) { C.spk_request_targets(h) })
}

func (gtkClipboard) Contents(ctx context.Context, target string) (io.ReadCloser, error) {
	d, err := ask(ctx, newPending(cData.free), func(h C.uintptr_t) {
		t := C.CString(target)
		defer C.free(unsafe.Pointer(t))
		C.spk_request_contents(h, t)
	})
	if err != nil {
		return nil, err
	}
	if d.n < 0 {
		return nil, errNoClipboardData
	}
	return newCReader(d.p, d.n, d.free), nil
}

// ImagePNG asks GTK for the clipboard's picture in any format it can load
// and encodes it as PNG with gdk-pixbuf — off the main thread (the pixbuf
// is ours alone by then).
func (gtkClipboard) ImagePNG(ctx context.Context) (io.ReadCloser, error) {
	pb, err := ask(ctx, newPending(func(pb unsafe.Pointer) {
		if pb != nil {
			C.spk_unref(pb)
		}
	}), func(h C.uintptr_t) { C.spk_request_image(h) })
	if err != nil {
		return nil, err
	}
	if pb == nil {
		return nil, errNoClipboardData
	}
	defer C.spk_unref(pb)
	var buf *C.gchar
	var size C.gsize
	if C.spk_pixbuf_png(pb, &buf, &size) == 0 {
		return nil, errors.New("gdk-pixbuf could not encode the picture as PNG")
	}
	return newCReader(unsafe.Pointer(buf), int(size), func() { C.g_free(C.gpointer(buf)) }), nil
}
