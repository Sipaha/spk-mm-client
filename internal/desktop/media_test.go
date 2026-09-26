package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithMediaRoutesMediaAndAssets(t *testing.T) {
	assets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("asset " + r.URL.Path)) })
	mediaH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("media " + r.URL.Path)) })
	serve := func(h http.Handler, path string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	h := withMedia(assets, mediaH)
	for path, want := range map[string]string{
		"/": "asset /", "/index.html": "asset /index.html", "/assets/app.js": "asset /assets/app.js",
		"/media/1/avatar/u1": "media /media/1/avatar/u1", "/media/1/stream/f1": "media /media/1/stream/f1",
	} {
		assert.Equal(t, want, serve(h, path), path)
	}
	assert.Equal(t, "asset /media/1/avatar/u1", serve(withMedia(assets, nil), "/media/1/avatar/u1"), "no cache: assets only")
}
