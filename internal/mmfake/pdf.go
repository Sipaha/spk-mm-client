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
)

// manualPDF is a well-formed PDF 1.4 of pages A4 pages: selectable text in
// Helvetica (a standard font, not embedded) and a few vector shapes per
// page, uncompressed — about 1.5 KB a page, since every in-process fake
// keeps it in memory. Object 1 is the catalog, 2 the page tree, 3 the font,
// then a page and its content stream per page.
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
		content := manualPage(p, pages)
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*(p-1)))
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
