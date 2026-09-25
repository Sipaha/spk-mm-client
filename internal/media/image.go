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

	"golang.org/x/image/draw"
)

// rasterTypes are what http.DetectContentType may say for an accepted
// image. SVG (text/xml) is never among them: it can carry scripts.
var rasterTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true}

const headPeek = 64 << 10

// writeImage copies an image to dst after checking it: a raster type by
// sniffing, at most sp.max bytes, at most maxPixels when the header is
// readable. Feed PNG/JPEG larger than FeedMax are scaled down.
func writeImage(dst io.Writer, src io.Reader, sp spec) (int64, error) {
	br := bufio.NewReaderSize(src, headPeek)
	head, err := br.Peek(headPeek)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	ctype := http.DetectContentType(head)
	if len(head) == 0 || !rasterTypes[ctype] {
		return 0, errType
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(head)); err == nil && int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return 0, errTooLarge
	}
	if sp.scale && (ctype == "image/png" || ctype == "image/jpeg") {
		data, err := io.ReadAll(io.LimitReader(br, sp.max+1))
		if err != nil {
			return 0, err
		}
		if int64(len(data)) > sp.max {
			return 0, errTooLarge
		}
		scaled, err := downscale(data, ctype)
		if err != nil {
			return 0, err
		}
		if scaled != nil {
			data = scaled
		}
		n, err := dst.Write(data)
		return int64(n), err
	}
	n, err := io.Copy(dst, io.LimitReader(br, sp.max+1))
	if err != nil {
		return n, err
	}
	if n > sp.max {
		return 0, errTooLarge
	}
	return n, nil
}

// downscale fits a PNG/JPEG into FeedMax×FeedMax, keeping its format (PNG
// keeps transparency); nil when it already fits.
func downscale(data []byte, ctype string) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errType
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, errTooLarge
	}
	if cfg.Width <= FeedMax && cfg.Height <= FeedMax {
		return nil, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errType
	}
	scale := min(float64(FeedMax)/float64(cfg.Width), float64(FeedMax)/float64(cfg.Height))
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if ctype == "image/png" {
		err = png.Encode(&out, dst)
	} else {
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85})
	}
	return out.Bytes(), err
}

// writeText stores up to TextLimit bytes of a text file behind a one-byte
// flag ('T' — there is more, 'F' — complete). Content with NUL bytes or
// invalid UTF-8 is refused: a binary file with a text name, or a legacy
// encoding we would show as garbage.
func writeText(dst io.Writer, src io.Reader, contentRange string) (int64, error) {
	buf, err := io.ReadAll(io.LimitReader(src, TextLimit+1))
	if err != nil {
		return 0, err
	}
	truncated := len(buf) > TextLimit || rangeTotal(contentRange) > TextLimit
	if len(buf) > TextLimit {
		buf = buf[:TextLimit]
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
