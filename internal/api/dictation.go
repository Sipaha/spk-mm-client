package api

import (
	"context"
	"errors"
	"os"

	"github.com/spk/spk-mm-client/internal/speech"
)

// TranscribeAudio returns editable text only; it never posts or saves audio.
func (s *Service) TranscribeAudio(ctx context.Context, audio string) (string, error) {
	ctx, done, err := s.life.start(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	endpoint := os.Getenv("SPK_MM_CLIENT_SPEECH_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8095"
	}
	text, err := speech.Transcribe(ctx, endpoint, audio)
	if ctx.Err() != nil {
		return "", coded(CodeCancelled, nil)
	}
	if errors.Is(err, speech.ErrAudio) {
		return "", coded(CodeInvalidArgument, nil)
	}
	if errors.Is(err, speech.ErrUnavailable) {
		return "", coded("speech_unavailable", nil)
	}
	if err != nil {
		return "", coded("speech_failed", nil)
	}
	return text, nil
}
