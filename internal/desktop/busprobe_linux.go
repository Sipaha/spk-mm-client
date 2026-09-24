//go:build wails && linux

package desktop

import (
	"log/slog"
	"os"

	"github.com/godbus/dbus/v5"
)

// sessionBusState probes the D-Bus session bus once, bounded by
// dbusProbeTimeout; run.go feeds the result to integrationsFor.
//
// Why probe at all: Wails v3 beta.25 dials the session bus synchronously and
// without a timeout in SingleInstance (inside application.New(), which then
// os.Exit(1)s on failure — uninterceptable), in the notifications service
// startup, and in SystemTray.Run (InvokeSync(dbus.SessionBus()) on the GTK
// main thread — a hung bus freezes the UI). A missing or hung bus (headless
// box, broken user session, sandbox) must degrade those features, not stop
// the app from starting or responding.
func sessionBusState() busState {
	st, err := probeBus(func() error {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return err
		}
		return conn.Close()
	}, dbusProbeTimeout)
	switch st {
	case busUnreachable:
		slog.Warn("D-Bus session bus unreachable: tray, single-instance and notifications disabled; closing the window quits", "err", err)
	case busTimedOut:
		slog.Warn("D-Bus session bus not answering: tray, single-instance and notifications disabled; closing the window quits", "timeout", dbusProbeTimeout)
	}
	return st
}

// cutOffSessionBus makes every later D-Bus client in this process (GLib's
// GApplication registration, a11y, Wails' theme monitor) and in WebKit's
// child processes fail fast instead of blocking on an unusable bus.
func cutOffSessionBus() {
	if err := os.Setenv("DBUS_SESSION_BUS_ADDRESS", deadBusAddress); err != nil {
		slog.Warn("could not cut off the D-Bus session bus", "err", err)
	}
}
