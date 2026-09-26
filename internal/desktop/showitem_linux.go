//go:build linux

package desktop

import (
	"context"

	"github.com/godbus/dbus/v5"
)

// showItems calls FileManager1.ShowItems on a private session bus
// connection, closed right after (like the startup probe: a hung or cut-off
// bus fails this call only).
//
// Edge cases, acceptable for a user action bounded by showItemTimeout:
//   - with DBUS_SESSION_BUS_ADDRESS unset, godbus falls back to
//     /run/user/<uid>/bus and then autolaunches dbus-launch, which does
//     not take ctx: it may outlive the timeout in the abandoned goroutine;
//   - a file manager activated slowly (the bus starting it after the
//     timeout) may still show the file after the caller has already
//     opened the folder: the user then sees both windows.
func showItems(ctx context.Context, uri string) error {
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1").
		CallWithContext(ctx, "org.freedesktop.FileManager1.ShowItems", 0, []string{uri}, "").Err
}
