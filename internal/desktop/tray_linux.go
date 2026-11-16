//go:build wails && linux

package desktop

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	sniInterface       = "org.kde.StatusNotifierItem"
	sniObjectPath      = dbus.ObjectPath("/StatusNotifierItem")
	trayReadyWarnAfter = 5 * time.Second
	trayPollFast       = 20 * time.Millisecond
	trayPollSlow       = 250 * time.Millisecond
	trayDBusCallLimit  = 2 * time.Second
)

type sniPixmap struct {
	W, H int32
	Pix  []byte
}

type sniToolTip struct {
	Name  string
	Icons []sniPixmap
	Title string
	Desc  string
}

type platformTray struct {
	ctx     context.Context
	busName string
	cli     *dbus.Conn
	sigConn *dbus.Conn
}

func newPlatformTray(ctx context.Context) *platformTray {
	return &platformTray{
		ctx:     ctx,
		busName: fmt.Sprintf("%s-%d-1", sniInterface, os.Getpid()),
	}
}

// waitReady waits until ApplicationStarted has fired, Wails' Linux SystemTray
// has exported /StatusNotifierItem properties on the session bus, and its
// InvokeSync(s.impl.run) setup call on the GTK main thread has returned.
// Slow startup or transient D-Bus errors log once and keep retrying until ctx
// ends so the badge consumer never exits permanently while the app runs.
func (p *platformTray) waitReady(ctx context.Context, started <-chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	case <-started:
	}

	var (
		cli       *dbus.Conn
		warnedBus bool
	)
	ready := waitUntilReady(
		ctx,
		trayReadyWarnAfter,
		trayPollFast,
		trayPollSlow,
		func(c context.Context) bool {
			if cli == nil {
				conn, err := dbus.ConnectSessionBus()
				if err != nil {
					if !warnedBus && c.Err() == nil {
						warnedBus = true
						slog.Warn("tray D-Bus client not ready yet, retrying", "err", err)
					}
					return false
				}
				cli = conn
			}
			if !sniExported(c, cli, p.busName) {
				if !cli.Connected() {
					_ = cli.Close()
					cli = nil
				}
				return false
			}
			return true
		},
		func() {
			slog.Warn("system tray SNI properties still starting, continuing to wait", "name", p.busName, "elapsed", trayReadyWarnAfter)
		},
	)
	if !ready {
		if cli != nil {
			_ = cli.Close()
		}
		return false
	}

	p.cli = cli
	go func() {
		<-ctx.Done()
		_ = cli.Close()
	}()

	if sigConn, err := dbus.SessionBus(); err == nil {
		p.sigConn = sigConn
	}
	if ctx.Err() != nil {
		return false
	}
	// FIFO fence on the GTK main loop: ensures s.impl.run() (including its
	// trailing setLabel/setMenu calls) has completely finished before any
	// badge update calls SetLabel/SetIcon.
	application.InvokeSync(func() {})
	return ctx.Err() == nil
}

// syncTooltip ensures the exported org.kde.StatusNotifierItem.ToolTip property
// carries a single-line Title (with empty Description) and emits NewToolTip
// from the SNI owner connection. Wails v3.1.0 updates ToolTip via godbus prop
// PropertiesChanged inside SetLabel, but does not emit the SNI NewToolTip
// signal that StatusNotifierWatcher / xapp-sn-watcher / KDE hosts listen for.
func (p *platformTray) syncTooltip(text string) {
	if p.ctx.Err() != nil {
		return
	}
	if p.cli == nil || !p.cli.Connected() {
		if p.cli != nil {
			_ = p.cli.Close()
			p.cli = nil
		}
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return
		}
		p.cli = conn
		go func() {
			<-p.ctx.Done()
			_ = conn.Close()
		}()
	}
	if p.sigConn == nil {
		if sigConn, err := dbus.SessionBus(); err == nil {
			p.sigConn = sigConn
		}
	}
	if err := setSNIToolTip(p.ctx, p.cli, p.sigConn, p.busName, text); err != nil && p.ctx.Err() == nil {
		slog.Debug("could not update SNI ToolTip property", "err", err)
	}
}

func sniExported(ctx context.Context, cli *dbus.Conn, dest string) bool {
	cctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var v dbus.Variant
	err := cli.Object(dest, sniObjectPath).CallWithContext(
		cctx, "org.freedesktop.DBus.Properties.Get", 0, sniInterface, "Id",
	).Store(&v)
	return err == nil
}

func setSNIToolTip(ctx context.Context, cli, sigConn *dbus.Conn, dest, text string) error {
	cctx, cancel := context.WithTimeout(ctx, trayDBusCallLimit)
	defer cancel()
	val := dbus.MakeVariant(sniToolTip{Title: text})
	if call := cli.Object(dest, sniObjectPath).CallWithContext(
		cctx, "org.freedesktop.DBus.Properties.Set", 0, sniInterface, "ToolTip", val,
	); call.Err != nil {
		return call.Err
	}
	if sigConn != nil {
		return sigConn.Emit(sniObjectPath, sniInterface+".NewToolTip")
	}
	return nil
}
