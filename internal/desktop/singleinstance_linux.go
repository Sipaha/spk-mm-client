//go:build wails && linux

package desktop

import (
	"context"
	"log/slog"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// dbusProbeTimeout bounds how long we wait to confirm the D-Bus session bus
// is reachable before opting into Wails' SingleInstance feature. See
// singleInstanceOptions.
const dbusProbeTimeout = 2 * time.Second

// singleInstanceOptions returns a pointer to opts when the D-Bus session bus
// is reachable, or nil (i.e. "don't ask Wails for single-instance locking at
// all") otherwise.
//
// On Linux, Wails v3 beta.25's SingleInstance support (pkg/application/
// single_instance_linux.go) is implemented entirely on top of D-Bus: the
// first instance claims exclusivity by registering a session-bus name, and
// forwarding a second instance's argv to the first is a D-Bus method call.
// application.New() sets this up by calling dbus.ConnectSessionBus()
// synchronously — no timeout, no context — the moment SingleInstanceOptions
// is non-nil, and if that dial fails, application.New() calls its internal
// fatal() helper, which does os.Exit(1) *before application.New() even
// returns*. That is uninterceptable from run.go: no defer, no recover, no
// error return — the whole process is just gone. A missing/broken session
// bus (headless box, broken user session, sandboxed container without
// D-Bus) must never take the whole app down for that, per the "no blocking,
// timeout-less calls to system services on the startup path" constraint —
// so we probe the bus ourselves first, bounded by dbusProbeTimeout using the
// same startWithTimeout helper notifier.ServiceStartup uses (notify.go), and
// simply omit SingleInstanceOptions — degrading to "a second launch opens a
// second window instead of focusing the first", rather than "the app never
// starts" — when the bus is unreachable.
func singleInstanceOptions(opts application.SingleInstanceOptions) *application.SingleInstanceOptions {
	available, err := startWithTimeout(context.Background(), func(context.Context) error {
		conn, dialErr := dbus.ConnectSessionBus()
		if dialErr != nil {
			return dialErr
		}
		return conn.Close()
	}, dbusProbeTimeout)
	if available {
		return &opts
	}
	if err != nil {
		slog.Warn("single-instance locking disabled: D-Bus session bus unreachable", "err", err)
	} else {
		slog.Warn("single-instance locking disabled: D-Bus session bus probe timed out", "timeout", dbusProbeTimeout)
	}
	return nil
}
