package main

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/spk/spk-mm-client/internal/api"
)

// uploadHandler takes one file from the page (browser mode: pasted,
// dropped or picked) as the raw request body — no JSON, base64 or
// multipart — and spools it as an attachment of the channel's (or, with
// ?root=, a thread's reply) next message:
// POST /api/attachments/{srv}/{channel}?root=&name=&mime=. At most the
// server's MaxFileSize is read (413 over it); the store cleans the name
// (last path element, no control/format characters, ≤ 200 runes). The desktop never sends
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
		a, err := svc.AddAttachmentBytes(r.Context(), srv, r.PathValue("channel"), q.Get("root"), q.Get("name"), uploadMime(q.Get("mime")), body, limit)
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

// uploadMime keeps a well-formed media type ("" otherwise: the store
// takes it from the name or the content).
func uploadMime(raw string) string {
	mt, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return ""
	}
	return mt
}
