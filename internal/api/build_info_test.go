package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/spk/spk-mm-client/internal/events"
	"github.com/stretchr/testify/assert"
)

func TestAppInfoReportsConstructorBuildWithoutCredentials(t *testing.T) {
	s := NewService(nil, events.NewEmitter(), nil, &http.Client{}, BuildInfo{Version: "test-build", Mode: "desktop"})
	s.getenv = func(string) string { return "" }
	info, err := s.AppInfo(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "SPK MM Client", info.Name)
	assert.Equal(t, "test-build", info.Version)
	assert.Equal(t, "desktop", info.Mode)
	assert.Empty(t, info.FormatLocale)
}
