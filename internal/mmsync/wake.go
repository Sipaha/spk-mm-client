package mmsync

import (
	"context"
	"time"
)

// slept reports a suspend between two ticks: the wall clock kept running
// while the monotonic clock (which stops during suspend on Linux and
// Windows) did not.
func slept(wallDelta, monoDelta, threshold time.Duration) bool {
	return wallDelta-monoDelta > threshold
}

func watchWake(ctx context.Context, every time.Duration, fire func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	prev := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			if slept(now.Round(0).Sub(prev.Round(0)), now.Sub(prev), 2*every+5*time.Second) {
				fire()
			}
			prev = now
		}
	}
}
