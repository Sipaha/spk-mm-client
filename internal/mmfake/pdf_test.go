package mmfake

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/jpeg"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// The seeded manual is a well-formed multi-page PDF (the viewer's pdf.js
// renders it; the PDF memory check scrolls through it): every xref offset points
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

	require.True(t, bytes.HasPrefix(doc, []byte("%PDF-1.4\n")))
	require.True(t, bytes.HasSuffix(doc, []byte("%%EOF\n")))
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`).FindSubmatch(doc)
	require.NotNil(t, m)
	xref, err := strconv.Atoi(string(m[1]))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(doc[xref:], []byte("xref\n0 ")))
	entries := regexp.MustCompile(`(\d{10}) 00000 n \n`).FindAllSubmatch(doc[xref:], -1)
	require.Len(t, entries, 3+3*PDFPages, "catalog, page tree, font, and a page, its content and its picture per page")
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

// The PDF memory check (PDF Task 4) needs a document like the spike's 4.3 MB
// test50.pdf: every page carries its own photo-like 480x360 JPEG (pdf.js
// decodes one image per page, as in a scanned or illustrated manual), not
// one picture shared by all pages (pdf.js would cache and decode it once).
func TestSeededPDFIsImageHeavy(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	_, doc := a.raw("GET", "/api/v4/files/"+PDFFileID, nil)
	assert.Greater(t, len(doc), 3<<20, "comparable to the spike's 4.3 MB document")
	assert.Less(t, len(doc), 6<<20)

	re := regexp.MustCompile(`<< /Type /XObject /Subtype /Image /Width 480 /Height 360 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length (\d+) >>\nstream\n`)
	locs := re.FindAllSubmatchIndex(doc, -1)
	// PDFPages-1: ScannedPage carries a CCITT-encoded scan instead of a JPEG
	// (final review I3) — TestSeededPDFHasAScannedCCITTPage covers it.
	require.Len(t, locs, PDFPages-1, "one JPEG picture per page except the scanned one")
	seen := map[[32]byte]bool{}
	for _, l := range locs {
		n, err := strconv.Atoi(string(doc[l[2]:l[3]]))
		require.NoError(t, err)
		body := doc[l[1] : l[1]+n]
		require.True(t, bytes.HasPrefix(doc[l[1]+n:], []byte("\nendstream")), "the /Length is right")
		img, err := jpeg.Decode(bytes.NewReader(body))
		require.NoError(t, err)
		assert.Equal(t, 480, img.Bounds().Dx())
		assert.Equal(t, 360, img.Bounds().Dy())
		seen[sha256.Sum256(body)] = true
	}
	assert.Len(t, seen, PDFPages-1, "every JPEG page has a picture of its own")
	assert.Equal(t, PDFPages, bytes.Count(doc, []byte("/XObject << /Im1 ")), "each page's resources name its picture")
	assert.Equal(t, PDFPages, bytes.Count(doc, []byte("/Im1 Do")), "and each page draws it")
}

// TestSeededPDFHasAScannedCCITTPage: final review I3 — pdf.js 6.3 decodes
// CCITTFaxDecode/JBIG2 images through the jbig2.wasm module fetched from
// wasmUrl; without shipping and wiring that asset, a scanned page like this
// one renders blank. ScannedPage exercises the real end-to-end path (the
// fake serves it, PdfView.tsx must ask pdf.js to actually fetch the wasm).
func TestSeededPDFHasAScannedCCITTPage(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	_, doc := a.raw("GET", "/api/v4/files/"+PDFFileID, nil)

	re := regexp.MustCompile(`<< /Type /XObject /Subtype /Image /Width (\d+) /Height (\d+) /ColorSpace /DeviceGray /BitsPerComponent 1 ` +
		`/Filter /CCITTFaxDecode /DecodeParms << /K -1 /Columns (\d+) /Rows (\d+) /BlackIs1 true >> /Length (\d+) >>\nstream\n`)
	locs := re.FindAllSubmatchIndex(doc, -1)
	require.Len(t, locs, 1, "exactly one CCITT-encoded page")
	l := locs[0]
	for _, pair := range [][2][]byte{{doc[l[2]:l[3]], []byte(strconv.Itoa(scannedImageW))}, {doc[l[4]:l[5]], []byte(strconv.Itoa(scannedImageH))}} {
		assert.Equal(t, string(pair[1]), string(pair[0]))
	}
	n, err := strconv.Atoi(string(doc[l[10]:l[11]]))
	require.NoError(t, err)
	assert.Equal(t, len(scannedImageCCITT), n)
	body := doc[l[1] : l[1]+n]
	assert.Equal(t, scannedImageCCITT, body)
	require.True(t, bytes.HasPrefix(doc[l[1]+n:], []byte("\nendstream")), "the /Length is right")

	assert.NotEqual(t, 1, ScannedPage)
	assert.NotEqual(t, PDFPages, ScannedPage)
	assert.NotEqual(t, LandscapePage, ScannedPage)
}

// Every fake (a soak run starts several in one process) serves the same
// generated bytes: the document is built once per process, not per fake.
func TestSeededPDFIsBuiltOncePerProcess(t *testing.T) {
	a, b := Start(Options{}), Start(Options{})
	defer a.Close()
	defer b.Close()
	da, db := a.chat.files[PDFFileID].data, b.chat.files[PDFFileID].data
	require.NotEmpty(t, da)
	assert.Same(t, &da[0], &db[0])
}

// The seeded receipt is the everyday one-page PDF (ReceiptFileID): bob's DM
// carries it, it is a real one-page Letter document with vector text in
// embedded CID TrueType fonts and a small raster logo — the shape of the
// receipts the viewer's scroll/default-zoom fix was measured on — and it
// stays small.
func TestSeededReceiptIsAOnePageVectorPDF(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var info model.FileInfo
	require.Equal(t, 200, a.call("GET", "/api/v4/files/"+ReceiptFileID+"/info", nil, &info))
	assert.Equal(t, ReceiptName, info.Name)
	assert.Equal(t, "application/pdf", info.MimeType)
	var posts model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-dm-bob/posts?page=0&per_page=60", nil, &posts))
	var inDM []string
	for _, p := range posts.Ascending() {
		if p.Metadata != nil {
			for _, f := range p.Metadata.Files {
				inDM = append(inDM, f.ID)
			}
		}
	}
	assert.Equal(t, []string{ReceiptFileID}, inDM, "bob's DM carries the receipt")
	resp, doc := a.raw("GET", "/api/v4/files/"+ReceiptFileID, nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, info.Size, int64(len(doc)))
	assert.Less(t, len(doc), 64<<10, "a receipt-sized file")

	require.True(t, bytes.HasPrefix(doc, []byte("%PDF-")))
	assert.Regexp(t, `/Type /Pages\s*/Count 1\b`, string(doc), "one page")
	assert.Contains(t, string(doc), "/MediaBox [0 0 612 792]", "US Letter")
	assert.GreaterOrEqual(t, bytes.Count(doc, []byte("/Subtype /CIDFontType2")), 2, "embedded CID TrueType fonts")
	assert.Contains(t, string(doc), "/FontFile2", "the fonts are embedded")
	assert.Contains(t, string(doc), "/Subtype /Image", "a raster logo")
	assert.Contains(t, string(doc), "/Subtype /Link", "link annotations")
}
