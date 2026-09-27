package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestUploadFileSendsQueryContentLengthAndRawBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/files", r.URL.Path)
		assert.Equal(t, "c1", r.URL.Query().Get("channel_id"))
		assert.Equal(t, "note.txt", r.URL.Query().Get("filename"))
		assert.Equal(t, "att-1", r.URL.Query().Get("client_id"))
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.EqualValues(t, 5, r.ContentLength)
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(b))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"file_infos":[{"id":"f1","name":"note.txt","size":5,"user_id":"u1"}],"client_ids":["att-1"]}`))
	})
	fi, err := c.UploadFile(context.Background(), "c1", "note.txt", "att-1", strings.NewReader("hello"), 5, nil)
	require.NoError(t, err)
	assert.Equal(t, "f1", fi.ID)
	assert.Equal(t, "note.txt", fi.Name)
	assert.Equal(t, int64(5), fi.Size)
	assert.Equal(t, "u1", fi.UserID)
}

func TestUploadFileWithoutClientIDOmitsTheQueryParam(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.False(t, r.URL.Query().Has("client_id"))
		_, _ = w.Write([]byte(`{"file_infos":[{"id":"f1","name":"x","size":1}],"client_ids":[""]}`))
	})
	_, err := c.UploadFile(context.Background(), "c1", "x", "", strings.NewReader("x"), 1, nil)
	require.NoError(t, err)
}

func TestUploadFileReportsProgress(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"file_infos":[{"id":"f1","name":"x","size":9}],"client_ids":[""]}`))
	})
	var got []int64
	_, err := c.UploadFile(context.Background(), "c1", "x", "", strings.NewReader("123456789"), 9,
		func(sent int64) { got = append(got, sent) })
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, int64(9), got[len(got)-1], "the last progress call reports everything sent")
}

func TestUploadFileClassifiesErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   ErrKind
	}{
		{http.StatusForbidden, KindAuth},
		{http.StatusUnauthorized, KindAuth},
		{http.StatusRequestEntityTooLarge, KindTooLarge},
		{http.StatusInternalServerError, KindNetwork},
		{http.StatusBadRequest, KindAPI},
	} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"id":"some.app_error","message":"nope"}`))
		})
		_, err := c.UploadFile(context.Background(), "c1", "x", "", strings.NewReader("x"), 1, nil)
		var e *Error
		require.ErrorAs(t, err, &e, tc.status)
		assert.Equal(t, tc.kind, e.Kind, tc.status)
		assert.Equal(t, tc.status, e.Status, tc.status)
	}
}

func TestUploadFileIsNotRetriedOnServerError(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.UploadFile(context.Background(), "c1", "x", "", strings.NewReader("x"), 1, nil)
	require.Error(t, err)
	assert.Equal(t, int32(1), n.Load())
}

func TestUploadFileTakesALimiterToken(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"file_infos":[{"id":"f1","name":"x","size":1}],"client_ids":[""]}`))
	})
	lim := rate.NewLimiter(rate.Every(time.Hour), 1)
	_, err := c.WithLimiter(lim).UploadFile(context.Background(), "c1", "x", "", strings.NewReader("x"), 1, nil)
	require.NoError(t, err)
	assert.InDelta(t, 0, lim.Tokens(), 0.01, "the attempt took a token")
}
