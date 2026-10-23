// Package speech is a native client of the local LAIHub speech API.
package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const MaxWAV = 44 + 16000*2*60

var ErrAudio = errors.New("expected 0.1–60 seconds of PCM16 mono 16 kHz WAV")
var ErrUnavailable = errors.New("local speech service unavailable")
var ErrRecognition = errors.New("speech recognition failed")

// Transcribe sends only recorded audio, without Mattermost credentials, proxy,
// cookies or redirects. The endpoint is operator configuration, never renderer input.
func Transcribe(ctx context.Context, endpoint, encoded string) (string, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxWAV) {
		return "", ErrAudio
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !validWAV(data) {
		return "", ErrAudio
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", ErrUnavailable
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", ErrUnavailable
	}
	u.Path = "/v1/audio/transcriptions"
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "dictation.wav")
	if err != nil {
		return "", ErrRecognition
	}
	if _, err = part.Write(data); err != nil {
		return "", ErrRecognition
	}
	if err = form.WriteField("language", "ru"); err != nil {
		return "", ErrRecognition
	}
	if err = form.Close(); err != nil {
		return "", ErrRecognition
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return "", ErrUnavailable
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		return "", ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", ErrRecognition
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	if err != nil || len(b) > 64<<10 {
		return "", ErrRecognition
	}
	var result struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(b, &result) != nil || strings.TrimSpace(result.Text) == "" {
		return "", ErrRecognition
	}
	return strings.TrimSpace(result.Text), nil
}

func validWAV(b []byte) bool {
	if len(b) < 44+3200 || len(b) > MaxWAV || len(b)%2 != 0 {
		return false
	}
	v := func(i int) uint32 { return binary.LittleEndian.Uint32(b[i : i+4]) }
	w := func(i int) uint16 { return binary.LittleEndian.Uint16(b[i : i+2]) }
	return string(b[:4]) == "RIFF" && v(4) == uint32(len(b)-8) && string(b[8:16]) == "WAVEfmt " && v(16) == 16 && w(20) == 1 && w(22) == 1 && v(24) == 16000 && v(28) == 32000 && w(32) == 2 && w(34) == 16 && string(b[36:40]) == "data" && v(40) == uint32(len(b)-44)
}
