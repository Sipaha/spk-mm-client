package main

import (
	"embed"
	"io/fs"
	"mime"
)

//go:embed all:dist
var distFS embed.FS

// Go's built-in mime table has no .wasm entry (the OS mime.types some
// platforms fall back to may or may not, and Windows has none), so without
// this http.FileServer/AssetFileServerFS would sniff a Content-Type of
// application/octet-stream for the pdf.js wasm assets under dist/pdfjs/wasm
// (frontend/vite.config.ts's copyPdfjsAssets, PdfView.tsx's wasmUrl — final
// review I3). WebAssembly.instantiateStreaming requires exactly
// application/wasm; registering it once, process-wide, before any request
// is served, fixes both browser mode (frontendHandler) and the desktop
// asset server (application.AssetFileServerFS) — both are backed by Go's
// own mime package.
func init() {
	if err := mime.AddExtensionType(".wasm", "application/wasm"); err != nil {
		panic(err) // a hard-coded, always-valid extension/type pair
	}
}

// frontendFS returns the built SPA rooted at dist/. In a plain `go build`
// (no frontend built) dist holds only .gitkeep and the UI is empty — the
// Makefile copies frontend/dist here before building.
func frontendFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
