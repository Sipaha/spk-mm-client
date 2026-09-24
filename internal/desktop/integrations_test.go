package desktop

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProbeBus(t *testing.T) {
	st, err := probeBus(func() error { return nil }, 50*time.Millisecond)
	assert.Equal(t, busOK, st)
	assert.NoError(t, err)

	boom := errors.New("no such file")
	st, err = probeBus(func() error { return boom }, 50*time.Millisecond)
	assert.Equal(t, busUnreachable, st)
	assert.ErrorIs(t, err, boom)

	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	st, err = probeBus(func() error { <-block; return nil }, 50*time.Millisecond)
	assert.Equal(t, busTimedOut, st, "a hung bus (accepts, never answers)")
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
}

func TestIntegrationsFor(t *testing.T) {
	all := integrations{singleInstance: true, tray: true, notifications: true}
	none := integrations{cutOffBus: true}
	assert.Equal(t, all, integrationsFor("linux", busOK))
	// Unreachable OR hung: every D-Bus consumer is skipped — Wails' tray does
	// a timeout-less dbus.SessionBus() on the GTK main thread.
	assert.Equal(t, none, integrationsFor("linux", busUnreachable))
	assert.Equal(t, none, integrationsFor("linux", busTimedOut))
	// Windows/macOS never touch D-Bus.
	assert.Equal(t, all, integrationsFor("windows", busTimedOut))
	assert.Equal(t, all, integrationsFor("darwin", busUnreachable))

	assert.True(t, all.closeHides(), "with a tray, close hides the window")
	assert.False(t, none.closeHides(), "no tray = no way back, so close quits")
}
