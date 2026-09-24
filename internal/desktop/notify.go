//go:build wails

package desktop

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notifier wraps the Wails notification service (spike S2). onClick receives
// the Data map of the clicked notification.
type notifier struct {
	svc *notifications.NotificationService
}

func newNotifier(onClick func(data map[string]any)) *notifier {
	svc := notifications.New()
	svc.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			slog.Warn("notification response error", "err", r.Error)
			return
		}
		onClick(r.Response.UserInfo)
	})
	return &notifier{svc: svc}
}

func (n *notifier) test() {
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
