package desktop

import (
	"context"
	"time"
)

// busState is the outcome of probing the D-Bus session bus once at startup.
type busState int

const (
	busOK          busState = iota
	busUnreachable          // dial failed fast (no bus, bad address)
	busTimedOut             // hung bus: accepts the connection, never answers
)

// dbusProbeTimeout bounds the one startup probe of the session bus.
const dbusProbeTimeout = 2 * time.Second

// probeBus runs connect bounded by timeout. connect is typically a
// dbus.ConnectSessionBus()+Close(), which has no timeout of its own; on
// timeout its goroutine is abandoned (see startWithTimeout).
func probeBus(connect func() error, timeout time.Duration) (busState, error) {
	ok, err := startWithTimeout(context.Background(), func(context.Context) error { return connect() }, timeout)
	switch {
	case ok:
		return busOK, nil
	case err != nil:
		return busUnreachable, err
	default:
		return busTimedOut, nil
	}
}

// integrations says which OS integrations the desktop app may enable.
type integrations struct {
	singleInstance bool
	tray           bool
	notifications  bool
	// cutOffBus: point DBUS_SESSION_BUS_ADDRESS at a dead address for the
	// rest of the process (and its WebKit children) before GTK starts. GLib
	// itself connects to the session bus with no timeout — GApplication
	// registration inside g_application_run, a11y — so with a hung bus the
	// window would never appear even with every Wails D-Bus feature off.
	cutOffBus bool
}

// deadBusAddress can never be connected to (a path under /dev/null), so every
// D-Bus client fails immediately instead of waiting on a hung bus.
const deadBusAddress = "unix:path=/dev/null/spk-mattermost-dbus-disabled"

// integrationsFor decides, from one probe result, which D-Bus-backed Wails
// features are safe to turn on. On Linux, Wails v3 beta.25 dials D-Bus with
// no timeout in all three: SingleInstance inside application.New() (os.Exit
// on failure), notifications in ServiceStartup, and the system tray via
// InvokeSync(dbus.SessionBus()) on the GTK main thread — with a hung bus the
// last one freezes the whole UI. So unless the bus answered, all three are
// off and the bus is cut off for GLib too (see cutOffBus). Windows and macOS
// don't use D-Bus.
func integrationsFor(goos string, bus busState) integrations {
	if goos == "linux" && bus != busOK {
		return integrations{cutOffBus: true}
	}
	return integrations{singleInstance: true, tray: true, notifications: true}
}

// closeHides: closing the window hides it only when a tray can bring it
// back; without a tray, closing quits the app.
func (i integrations) closeHides() bool { return i.tray }
