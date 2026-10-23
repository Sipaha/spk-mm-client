package speech

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func audio(seconds int) []byte {
	b := make([]byte, 44+seconds*32000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}

func TestTranscriptionContractAndMaximumRecording(t *testing.T) {
	b := audio(60)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/audio/transcriptions", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("Origin"))
		require.NoError(t, r.ParseMultipartForm(4<<20))
		require.Equal(t, "ru", r.FormValue("language"))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close()
		got, err := io.ReadAll(f)
		require.NoError(t, err)
		require.Equal(t, b, got)
		_, _ = fmt.Fprint(w, `{"text":"  проверка диктовки  ","elapsed_ms":12}`)
	}))
	defer srv.Close()
	// Explicitly bypass even an invalid operator proxy.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	got, err := Transcribe(context.Background(), srv.URL, base64.StdEncoding.EncodeToString(b))
	require.NoError(t, err)
	require.Equal(t, "проверка диктовки", got)
}

func TestRejectsInvalidAudioAndNonlocalEndpoints(t *testing.T) {
	b := audio(1)
	for _, v := range []string{"", "bad!", base64.StdEncoding.EncodeToString(audio(61)), base64.StdEncoding.EncodeToString(b[:100])} {
		_, err := Transcribe(context.Background(), "http://127.0.0.1:1", v)
		require.ErrorIs(t, err, ErrAudio)
	}
	b[24] = 0
	_, err := Transcribe(context.Background(), "http://127.0.0.1:1", base64.StdEncoding.EncodeToString(b))
	require.ErrorIs(t, err, ErrAudio)
	for _, v := range []string{"http://example.com", "http://localhost:8095", "http://user:password@127.0.0.1:8095", "https://127.0.0.1", "http://127.0.0.1/path", "http://127.0.0.1?x=y"} {
		_, err := Transcribe(context.Background(), v, base64.StdEncoding.EncodeToString(audio(1)))
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

func TestDoesNotFollowRedirectsOrAcceptEmptyOrLargeResponses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	for _, reply := range []string{"redirect", `{"text":" "}`, `not JSON`, string(make([]byte, 65537))} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reply == "redirect" {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				return
			}
			_, _ = fmt.Fprint(w, reply)
		}))
		_, err := Transcribe(context.Background(), srv.URL, base64.StdEncoding.EncodeToString(audio(1)))
		require.ErrorIs(t, err, ErrRecognition)
		srv.Close()
	}
}

func TestCancellationReachesSpeechServer(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Transcribe(ctx, srv.URL, base64.StdEncoding.EncodeToString(audio(1)))
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not cancel")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe cancellation")
	}
}
