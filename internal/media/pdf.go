package media

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// A PDF (KindPDF) is fetched whole for the viewer's pdf.js, which parses it
// in the web process. Go only checks the magic and the size and never reads
// the document into memory: it is copied to the cache as it comes and
// served from there. It is never shown by the webview itself — WebKitGTK's
// built-in viewer runs an old pdf.js with eval and PDF scripting on — so it
// is served as an attachment under the sandbox CSP (which keeps that
// viewer from running even if a frame navigates here).

const (
	// pdfMagicWithin: "%PDF-" must be within a PDF's first bytes, as
	// readers accept it (some producers put junk before the header).
	pdfMagicWithin = 1024
	// pdfTimeoutFactor stretches Options.Timeout for a PDF fetch: up to
	// PDFMax, twice a picture's cap (60 s → 5 min: ~1.4 Mbit/s for 50 MiB).
	pdfTimeoutFactor = 5
)

var pdfMagic = []byte("%PDF-")

// isPDF reports whether head (a file's first bytes) has the PDF header
// within pdfMagicWithin bytes.
func isPDF(head []byte) bool {
	return bytes.Contains(head[:min(len(head), pdfMagicWithin)], pdfMagic)
}

// writePDF copies a PDF of at most limit bytes from src to dst as it comes:
// the header is checked on the first bytes (errType), the size while copying
// (errTooLarge).
func writePDF(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	br := bufio.NewReaderSize(src, pdfMagicWithin)
	head, err := br.Peek(pdfMagicWithin)
	if !isPDF(head) {
		if err != nil && !errors.Is(err, io.EOF) { // a broken download, not a verdict on the file
			return 0, err
		}
		return 0, errType
	}
	n, err := io.Copy(dst, io.LimitReader(br, limit+1))
	switch {
	case err != nil:
		return 0, err
	case n > limit:
		return 0, errTooLarge
	}
	return n, nil
}
