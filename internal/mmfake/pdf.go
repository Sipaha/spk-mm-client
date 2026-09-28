package mmfake

import (
	"bytes"
	"fmt"
	"strings"
)

// Seeded PDFs in c-offtopic (for the viewer's PDF preview).
const (
	PDFFileID     = "f-manual" // manual.pdf: a real PDFPages-page document
	PDFPages      = 50
	JunkPDFFileID = "f-spec" // spec.pdf: a PDF header and nothing a reader can open
	// LandscapePage is the one page of manual.pdf turned sideways (A4
	// landscape instead of portrait) — real PDFs mix page sizes/orientations
	// (a scanned cover, an embedded slide, an appendix), and PdfView.tsx (fix
	// round 1, review of commit cffdfe9) must size each page from its own
	// viewport instead of stretching every page into page 1's box. Exercises
	// that both in the memory gate (Task 4) and in e2e (Task 5).
	LandscapePage = 2
)

// manualPDF is a well-formed PDF 1.4 of pages A4 pages (all portrait except
// LandscapePage, which is landscape): selectable text in Helvetica (a
// standard font, not embedded) and a few vector shapes per page,
// uncompressed — about 1.5 KB a page, since every in-process fake keeps it
// in memory. Object 1 is the catalog, 2 the page tree, 3 the font, then a
// page and its content stream per page.
func manualPDF(pages int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := []int{}
	obj := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	kids := make([]string, pages)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	for p := 1; p <= pages; p++ {
		w, h, content := 595, 842, manualPage(p, pages)
		if p == LandscapePage {
			w, h, content = 842, 595, manualLandscapePage(p, pages)
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", w, h, 5+2*(p-1)))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}

// manualPage is the content stream of page p: a coloured header band, the
// title, a paragraph and a framed box — enough to see pages apart and to
// select text.
func manualPage(p, pages int) string {
	var s strings.Builder
	hue := float64(p%6) / 6
	fmt.Fprintf(&s, "%.2f 0.45 %.2f rg 0 782 595 60 re f\n", 0.2+0.6*hue, 0.8-0.6*hue)
	fmt.Fprintf(&s, "BT /F1 28 Tf 1 1 1 rg 50 800 Td (spk-mm-client manual) Tj ET\n")
	fmt.Fprintf(&s, "BT /F1 18 Tf 0 0 0 rg 50 740 Td (Page %d of %d) Tj ET\n", p, pages)
	fmt.Fprintf(&s, "BT /F1 11 Tf 0.2 0.2 0.2 rg 50 700 Td 16 TL\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&s, "(Line %d of page %d: the quick brown fox jumps over the lazy dog.) '\n", i, p)
	}
	s.WriteString("ET\n")
	fmt.Fprintf(&s, "0.1 0.1 0.1 RG 2 w 50 150 495 300 re S\n")
	fmt.Fprintf(&s, "%.2f 0.6 0.3 rg %d 200 120 120 re f", 0.9-0.6*hue, 60+(p*37)%300)
	return s.String()
}

// manualLandscapePage is LandscapePage's own content stream, laid out for
// an 842x595 (landscape A4) page rather than manualPage's portrait one —
// its Y coordinates would otherwise land above the shorter page (a title at
// y=800 does not exist on a page only 595 tall).
func manualLandscapePage(p, pages int) string {
	var s strings.Builder
	s.WriteString("0.3 0.5 0.8 rg 0 535 842 60 re f\n")
	s.WriteString("BT /F1 24 Tf 1 1 1 rg 50 555 Td (spk-mm-client manual \\(landscape page\\)) Tj ET\n")
	fmt.Fprintf(&s, "BT /F1 16 Tf 0 0 0 rg 50 490 Td (Page %d of %d, landscape) Tj ET\n", p, pages)
	s.WriteString("BT /F1 11 Tf 0.2 0.2 0.2 rg 50 450 Td 16 TL\n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&s, "(Landscape line %d: a wide page in a portrait document.) '\n", i)
	}
	s.WriteString("ET\n")
	s.WriteString("0.1 0.1 0.1 RG 2 w 50 100 742 220 re S\n") // below the 5 text lines (they end around y=386)
	return s.String()
}
