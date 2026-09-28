package mmfake

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// The seeded manual is a well-formed multi-page PDF (the viewer's pdf.js
// renders it; the memory gate scrolls through it): every xref offset points
// at its object, the page tree counts PDFPages pages, and each page says
// which it is. spec.pdf stays junk behind a valid header (JunkPDFFileID).
func TestSeededPDFIsARealMultiPageDocument(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var info model.FileInfo
	require.Equal(t, 200, a.call("GET", "/api/v4/files/"+PDFFileID+"/info", nil, &info))
	assert.Equal(t, "manual.pdf", info.Name)
	assert.Equal(t, "application/pdf", info.MimeType)
	resp, doc := a.raw("GET", "/api/v4/files/"+PDFFileID, nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, info.Size, int64(len(doc)))
	assert.Less(t, len(doc), 256<<10, "small: the fakes keep it in memory")

	require.True(t, bytes.HasPrefix(doc, []byte("%PDF-1.4\n")))
	require.True(t, bytes.HasSuffix(doc, []byte("%%EOF\n")))
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`).FindSubmatch(doc)
	require.NotNil(t, m)
	xref, err := strconv.Atoi(string(m[1]))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(doc[xref:], []byte("xref\n0 ")))
	entries := regexp.MustCompile(`(\d{10}) 00000 n \n`).FindAllSubmatch(doc[xref:], -1)
	require.Len(t, entries, 3+2*PDFPages, "catalog, page tree, font, and a page plus its content per page")
	for i, e := range entries {
		off, _ := strconv.Atoi(string(e[1]))
		assert.True(t, bytes.HasPrefix(doc[off:], fmt.Appendf(nil, "%d 0 obj\n", i+1)), "object %d at %d", i+1, off)
	}
	assert.Contains(t, string(doc), fmt.Sprintf("/Count %d", PDFPages))
	assert.Equal(t, PDFPages, bytes.Count(doc, []byte("/Type /Page ")))
	for _, p := range []int{1, PDFPages} {
		assert.Contains(t, string(doc), fmt.Sprintf("(Page %d of %d) Tj", p, PDFPages))
	}

	resp, junk := a.raw("GET", "/api/v4/files/"+JunkPDFFileID, nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(junk, []byte("%PDF-")), "passes the client's magic check")
	assert.NotContains(t, string(junk), "xref", "but it is no document")
}

// TestSeededPDFHasOneLandscapePage: fix round 1 (review of cffdfe9) —
// PdfView.tsx must size each page from its own viewport instead of page 1's,
// so the fake exercises a document with one page turned sideways.
func TestSeededPDFHasOneLandscapePage(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	_, doc := a.raw("GET", "/api/v4/files/"+PDFFileID, nil)

	assert.Equal(t, PDFPages-1, bytes.Count(doc, []byte("/MediaBox [0 0 595 842]")), "every page but one stays portrait A4")
	assert.Equal(t, 1, bytes.Count(doc, []byte("/MediaBox [0 0 842 595]")), "exactly one page is landscape A4")
	assert.Contains(t, string(doc), fmt.Sprintf("Page %d of %d, landscape", LandscapePage, PDFPages))
	// Pages the existing test above already pins by exact "(Page %d of %d) Tj"
	// text (1 and PDFPages) must stay portrait, not collide with LandscapePage.
	assert.NotEqual(t, 1, LandscapePage)
	assert.NotEqual(t, PDFPages, LandscapePage)
}
