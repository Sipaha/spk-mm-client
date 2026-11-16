//go:build wails && !linux

package desktop

import (
	"context"
	"time"
)

type platformTray struct{}

func newPlatformTray(context.Context) *platformTray { return &platformTray{} }

func (*platformTray) waitReady(ctx context.Context, started <-chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	case <-started:
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(200 * time.Millisecond):
		return true
	}
}

func (*platformTray) syncTooltip(string) {}
