package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserFlagsRouteToBrowserRunner(t *testing.T) {
	var got browserOpts
	var desktopCalled bool
	cmd := newRootCmd(runners{
		browser: func(_ context.Context, o browserOpts) error { got = o; return nil },
		desktop: func(context.Context, desktopOpts) error { desktopCalled = true; return nil },
	})
	cmd.SetArgs([]string{"--browser", "--port", "5199", "--mm-fake", "--test-api"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.Equal(t, browserOpts{Port: 5199, MMFake: true, TestAPI: true}, got)
	assert.False(t, desktopCalled)
}

func TestNoFlagsRouteToDesktopRunner(t *testing.T) {
	var desktopCalled bool
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { t.Fatal("browser runner called"); return nil },
		desktop: func(context.Context, desktopOpts) error { desktopCalled = true; return nil },
	})
	cmd.SetArgs(nil)
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.True(t, desktopCalled)
}

// Linux/Windows pass a custom-scheme URL as the only argument when the OS
// launches us for mmauth://. Cobra must not reject it as an unknown command.
func TestPositionalDeepLinkArgIsAccepted(t *testing.T) {
	var desktopCalled bool
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { return nil },
		desktop: func(context.Context, desktopOpts) error { desktopCalled = true; return nil },
	})
	cmd.SetArgs([]string{"mmauth://callback?MMAUTHTOKEN=x&srv=https://mm.example.com"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.True(t, desktopCalled)
}

func TestDesktopDevFakeFlags(t *testing.T) {
	var got desktopOpts
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { t.Fatal("browser runner called"); return nil },
		desktop: func(_ context.Context, o desktopOpts) error { got = o; return nil },
	})
	cmd.SetArgs([]string{"--mm-fake", "--mm-fake-channels", "100"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.Equal(t, desktopOpts{MMFake: true, FakeChannels: 100}, got)
}
