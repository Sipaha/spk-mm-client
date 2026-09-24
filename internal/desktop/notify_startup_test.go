package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStartWithTimeoutSuccess(t *testing.T) {
	available, err := startWithTimeout(context.Background(), func(context.Context) error {
		return nil
	}, 50*time.Millisecond)
	assert.True(t, available)
	assert.NoError(t, err)
}

func TestStartWithTimeoutError(t *testing.T) {
	want := errors.New("boom")
	available, err := startWithTimeout(context.Background(), func(context.Context) error {
		return want
	}, 50*time.Millisecond)
	assert.False(t, available)
	assert.Equal(t, want, err)
}

// A start that never returns in time (e.g. a wedged dbus.ConnectSessionBus)
// must be reported as unavailable without an error — a timeout is not a
// failure the caller should log as "backend returned an error", just
// "backend never showed up in time".
func TestStartWithTimeoutTimesOutWithoutError(t *testing.T) {
	available, err := startWithTimeout(context.Background(), func(context.Context) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}, 20*time.Millisecond)
	assert.False(t, available)
	assert.NoError(t, err)
}
