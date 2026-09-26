//go:build linux

package desktop

import (
	"context"

	"github.com/godbus/dbus/v5"
)

// showItems calls FileManager1.ShowItems on a private session bus
// connection, closed right after (like the startup probe: a hung or cut-off
// bus fails this call only).
func showItems(ctx context.Context, uri string) error {
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1").
		CallWithContext(ctx, "org.freedesktop.FileManager1.ShowItems", 0, []string{uri}, "").Err
}
