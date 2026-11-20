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

func watchWake(ctx context.Context, every time.Duration, fire func(preSleep time.Time)) {
	t := time.NewTicker(every)
	defer t.Stop()
	threshold := 2*every + 5*time.Second
	watchWakeLoop(ctx, t.C, time.Now, func(prev, now time.Time) bool {
		return slept(now.Round(0).Sub(prev.Round(0)), now.Sub(prev), threshold)
	}, fire)
}

func watchWakeLoop(ctx context.Context, ticks <-chan time.Time, nowFn func() time.Time, didSleep func(prev, now time.Time) bool, fire func(preSleep time.Time)) {
	prev := nowFn()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			now := nowFn()
			if didSleep(prev, now) {
				fire(prev)
			}
			prev = now
		}
	}
}
