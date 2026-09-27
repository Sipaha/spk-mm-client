package media

import (
	"bufio"
	"bytes"
	"errors"
	"image"
	_ "image/gif" // DecodeConfig of GIF headers
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	_ "golang.org/x/image/bmp" // DecodeConfig of BMP headers
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // DecodeConfig of WebP headers
)

// rasterTypes are what http.DetectContentType may say for an accepted
// image. SVG (text/xml) is never among them: it can carry scripts.
var rasterTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true}

// IsRaster reports whether a media type is one of the raster pictures the
// cache shows (a staged attachment gets a preview only then).
func IsRaster(mime string) bool { return rasterTypes[mime] }

// sniffLen is what http.DetectContentType looks at.
const sniffLen = 512

// decodeSem lets one feed image be decoded and scaled at a time: a decode
// holds the whole bitmap (up to scaleMaxPixels × 4 bytes) in memory.
var decodeSem = make(chan struct{}, 1)

// decodeHook runs inside decodeSem before a decode (tests watch overlap).
var decodeHook = func() {}

// countWriter counts what went through to w.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// countReader counts what was read through it.
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// writeImage copies an image to dst after checking it: a raster type by
// sniffing, at most sp.max bytes, a readable header (fail closed: an image
// whose dimensions cannot be read is refused) of at most maxPixels. Feed
// images above scaleMaxPixels are refused; feed PNG/JPEG larger than FeedMax
// are scaled down. The file is streamed, never read into memory whole
// (attachments are not held in the Go heap): only the header the decoder
// needed is kept to replay, and scaling holds the bitmaps, not the file.
func writeImage(dst io.Writer, src io.Reader, sp spec) (int64, error) {
	br := bufio.NewReaderSize(src, sniffLen)
	head, err := br.Peek(sniffLen)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	ctype := http.DetectContentType(head)
	if len(head) == 0 || !rasterTypes[ctype] {
		return 0, errType
	}
	body := &countReader{r: io.LimitReader(br, sp.max+1)}
	// The header is read from the stream itself (a JPEG may carry EXIF far
	// beyond the first 64 KiB) and kept to be replayed.
	var hdr bytes.Buffer
	cfg, _, err := image.DecodeConfig(io.TeeReader(body, &hdr))
	switch {
	case body.n > sp.max:
		return 0, errTooLarge
	case err != nil:
		return 0, errType
	}
	if err := checkPixels(cfg, sp); err != nil {
		return 0, err
	}
	whole := io.MultiReader(&hdr, body)
	if sp.scale && (ctype == "image/png" || ctype == "image/jpeg") && (cfg.Width > FeedMax || cfg.Height > FeedMax) {
		return downscale(dst, whole, body, cfg, ctype, sp.max)
	}
	cw := &countWriter{w: dst}
	if _, err := io.Copy(cw, whole); err != nil {
		return 0, err
	}
	if body.n > sp.max {
		return 0, errTooLarge
	}
	return cw.n, nil
}

// checkPixels refuses decompression bombs, and feed images too large to
// scale (the UI asks for the original in the feed only when the file has no
// preview).
func checkPixels(cfg image.Config, sp spec) error {
	px := int64(cfg.Width) * int64(cfg.Height)
	if px > maxPixels || (sp.scale && px > scaleMaxPixels) {
		return errTooLarge
	}
	return nil
}

// downscale decodes a PNG/JPEG of cfg's size from src and writes it to dst
// fitted into FeedMax×FeedMax, keeping its format (PNG keeps transparency).
// body is the counted stream under src: what the decoder leaves unread is
// drained so the byte cap holds as for a copied file.
func downscale(dst io.Writer, src io.Reader, body *countReader, cfg image.Config, ctype string, limit int64) (int64, error) {
	decodeSem <- struct{}{}
	defer func() { <-decodeSem }()
	decodeHook()
	var img image.Image
	var err error
	if ctype == "image/png" {
		img, err = png.Decode(src)
	} else {
		img, err = jpeg.Decode(src)
	}
	if _, derr := io.Copy(io.Discard, body); derr != nil {
		return 0, derr
	}
	switch {
	case body.n > limit:
		return 0, errTooLarge
	case err != nil:
		return 0, errType
	}
	scale := min(float64(FeedMax)/float64(cfg.Width), float64(FeedMax)/float64(cfg.Height))
	out := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))))
	draw.ApproxBiLinear.Scale(out, out.Bounds(), img, img.Bounds(), draw.Src, nil)
	cw := &countWriter{w: dst}
	if ctype == "image/png" {
		err = png.Encode(cw, out)
	} else {
		err = jpeg.Encode(cw, out, &jpeg.Options{Quality: 85})
	}
	return cw.n, err
}

// writeText stores up to limit bytes of a text file (TextLimit for the feed
// snippet, TextFullLimit for the viewer's ?full=1) behind a one-byte flag
// ('T' — there is more, 'F' — complete). Content with NUL bytes or invalid
// UTF-8 is refused: a binary file with a text name, or a legacy encoding we
// would show as garbage.
func writeText(dst io.Writer, src io.Reader, contentRange string, limit int64) (int64, error) {
	buf, err := io.ReadAll(io.LimitReader(src, limit+1))
	if err != nil {
		return 0, err
	}
	truncated := int64(len(buf)) > limit || rangeTotal(contentRange) > limit
	if int64(len(buf)) > limit {
		buf = buf[:limit]
	}
	if truncated {
		buf = trimPartialRune(buf)
	}
	if bytes.IndexByte(buf, 0) >= 0 || !utf8.Valid(buf) {
		return 0, errType
	}
	flag := byte('F')
	if truncated {
		flag = 'T'
	}
	if _, err := dst.Write([]byte{flag}); err != nil {
		return 0, err
	}
	n, err := dst.Write(buf)
	return int64(n) + 1, err
}

// trimPartialRune drops a multi-byte character cut in half at the end.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return b
}

// rangeTotal reads the full size from "bytes 0-65535/200000"; -1 if unknown.
func rangeTotal(h string) int64 {
	_, total, ok := strings.Cut(h, "/")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
