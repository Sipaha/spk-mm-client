package main

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spk/spk-mm-client/internal/api"
)

// maxUploadName bounds a file name from the page, in runes.
const maxUploadName = 200

// uploadHandler takes one file from the page (browser mode: pasted,
// dropped or picked) as the raw request body — no JSON, base64 or
// multipart — and spools it as an attachment of the channel's next
// message: POST /api/attachments/{srv}/{channel}?name=&mime=. At most the
// server's MaxFileSize is read (413 over it). The desktop never sends
// bytes: there Go reads the clipboard and the files itself.
func uploadHandler(svc *api.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv, err := strconv.ParseInt(r.PathValue("srv"), 10, 64)
		if err != nil {
			writeUploadError(w, http.StatusBadRequest, &api.CodedError{Code: api.CodeNotFound, Detail: "bad server id"})
			return
		}
		limit := svc.AttachmentSizeLimit(srv)
		if r.ContentLength > limit {
			writeUploadError(w, http.StatusRequestEntityTooLarge, &api.CodedError{Code: api.CodeTooLarge})
			return
		}
		body := &limitedBody{r: http.MaxBytesReader(w, r.Body, limit)}
		q := r.URL.Query()
		a, err := svc.AddAttachmentBytes(r.Context(), srv, r.PathValue("channel"), uploadName(q.Get("name")), uploadMime(q.Get("mime")), body, limit)
		if err != nil {
			var ce *api.CodedError
			if !errors.As(err, &ce) {
				ce = &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
			}
			if body.over || ce.Code == api.CodeTooLarge {
				writeUploadError(w, http.StatusRequestEntityTooLarge, &api.CodedError{Code: api.CodeTooLarge})
				return
			}
			writeUploadError(w, http.StatusBadRequest, ce)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a)
	})
}

func writeUploadError(w http.ResponseWriter, status int, ce *api.CodedError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": ce.Code, "detail": ce.Detail})
}

// limitedBody notes that http.MaxBytesReader cut the body off.
type limitedBody struct {
	r    io.Reader
	over bool
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		b.over = true
	}
	return n, err
}

// uploadName cleans a file name from the page: its last path element,
// without control or bidi-override characters (a name must read as what
// it is), at most maxUploadName runes (the extension kept).
func uploadName(raw string) string {
	raw = raw[strings.LastIndexAny(raw, `/\`)+1:]
	var b strings.Builder
	for _, r := range raw {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			continue
		}
		b.WriteRune(r)
	}
	name := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(name) <= maxUploadName {
		return name
	}
	ext := filepath.Ext(name)
	if utf8.RuneCountInString(ext) > 16 {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	return string(stem[:maxUploadName-utf8.RuneCountInString(ext)]) + ext
}

// uploadMime keeps a well-formed media type ("" otherwise: the store
// takes it from the name or the content).
func uploadMime(raw string) string {
	mt, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return ""
	}
	return mt
}
