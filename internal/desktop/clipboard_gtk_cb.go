//go:build wails && gtk3

package desktop

/*
#include <stdint.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// Callbacks from GTK, on the main thread: the answers to the requests of
// clipboard_gtk.go and the native observers of observe_gtk.go. They hand
// the result over and return at once.

//export spkClipTargets
func spkClipTargets(h C.uintptr_t, names **C.char, n C.int) {
	handle := cgo.Handle(h)
	p := handle.Value().(*pending[[]string])
	handle.Delete()
	out := make([]string, 0, int(n))
	if n > 0 {
		for _, s := range unsafe.Slice(names, int(n)) {
			if s != nil {
				out = append(out, C.GoString(s))
			}
		}
	}
	p.answer(out)
}

//export spkClipContents
func spkClipContents(h C.uintptr_t, data unsafe.Pointer, n C.int) {
	handle := cgo.Handle(h)
	p := handle.Value().(*pending[cData])
	handle.Delete()
	p.answer(cData{p: data, n: int(n)})
}

//export spkClipImage
func spkClipImage(h C.uintptr_t, pixbuf unsafe.Pointer) {
	handle := cgo.Handle(h)
	p := handle.Value().(*pending[unsafe.Pointer])
	handle.Delete()
	p.answer(pixbuf)
}

//export spkNativeDrop
func spkNativeDrop(paths **C.char, n C.int) {
	out := make([]string, 0, int(n))
	if n > 0 {
		for _, s := range unsafe.Slice(paths, int(n)) {
			out = append(out, C.GoString(s))
		}
	}
	nativeDrops.record(out)
}

//export spkPasteKey
func spkPasteKey() { pasteKeys.press() }
