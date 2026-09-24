//go:build wails

package desktop

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notifyStartupTimeout bounds how long we wait for the OS notification
// backend (e.g. the D-Bus session bus on Linux) to come up before treating
// notifications as unavailable. See notifier.ServiceStartup.
const notifyStartupTimeout = 2 * time.Second

// notifier wraps the Wails notification service (spike S2). onClick receives
// the Data map of the clicked notification.
//
// notifier — not the raw *notifications.NotificationService — is what gets
// registered as an application.Service in run.go. notifications.New()'s own
// ServiceStartup dials the OS notification backend synchronously (on Linux,
// dbus.ConnectSessionBus, uncancellable and with no timeout of its own) and
// Wails aborts application.App.Run() outright if a Service's ServiceStartup
// returns a non-nil error. A missing/hung session bus (headless box, broken
// user session, sandboxed container) must never take the whole app down for
// that: notifier.ServiceStartup runs the real startup in a goroutine bounded
// by notifyStartupTimeout (see startWithTimeout, notify_startup.go) and
// always returns nil itself — on error or timeout it just logs a warning
// and marks notifications unavailable, degrading test()/sends to no-ops.
type notifier struct {
	svc       *notifications.NotificationService
	available atomic.Bool
}

func newNotifier(onClick func(data map[string]any)) *notifier {
	svc := notifications.New()
	n := &notifier{svc: svc}
	svc.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			slog.Warn("notification response error", "err", r.Error)
			return
		}
		onClick(r.Response.UserInfo)
	})
	return n
}

// ServiceStartup implements application.ServiceStartup. It never returns an
// error: a failing or slow notification backend degrades notifications to a
// no-op instead of aborting application startup.
func (n *notifier) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	available, err := startWithTimeout(ctx, func(c context.Context) error {
		return n.svc.ServiceStartup(c, options)
	}, notifyStartupTimeout)
	n.available.Store(available)
	switch {
	case available:
	case err != nil:
		slog.Warn("notifications unavailable: backend startup failed", "err", err)
	default:
		slog.Warn("notifications unavailable: backend startup timed out", "timeout", notifyStartupTimeout)
	}
	return nil
}

// ServiceShutdown implements application.ServiceShutdown. Only delegates to
// the inner service if its startup actually completed — an inner service
// that never finished (or failed) starting up may hold partially
// initialised state its own Shutdown does not expect.
func (n *notifier) ServiceShutdown() error {
	if !n.available.Load() {
		return nil
	}
	return n.svc.ServiceShutdown()
}

func (n *notifier) test() {
	if !n.available.Load() {
		slog.Info("test notification skipped: notifications unavailable")
		return
	}
	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	err := n.svc.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: "spk-mattermost",
		Body:  "Тестовое уведомление — кликните, чтобы открыть окно",
		Data:  map[string]any{"target": "test", "id": id},
	})
	if err != nil {
		slog.Warn("test notification failed", "err", err)
	}
}
