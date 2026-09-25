package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"strings"
	"time"

	"github.com/spk/spk-mattermost/internal/mmfake"
)

// fakeChurn drives a dev desktop run for memory soak tests
// (--mm-fake-churn): every tick bob posts to a random channel
// of a random fake server, and every few ticks the UI is asked to open a
// random channel (the same path a notification click takes), so the feed,
// markdown rendering and channel switching all keep running for hours.
// Posts carry no @mention: the soak must not flood the desktop with
// notifications.
type fakeChurn struct {
	every     time.Duration
	fakes     []*mmfake.Server
	serverIDs []int64 // server id of fakes[i] in the service
	channels  int     // extra "c-load-NNN" channels per fake
	open      func(serverID int64, channelID string)
}

const churnSwitchEvery = 5 // ticks between channel switches

var churnWords = strings.Fields("sync window feed cache render **bold** _italic_ `code` [link](https://example.com) memory budget webkit gtk channel server")

func runFakeChurn(ctx context.Context, c fakeChurn) {
	t := time.NewTicker(c.every)
	defer t.Stop()
	stats := time.NewTicker(time.Minute)
	defer stats.Stop()
	channel := func() string {
		if c.channels == 0 || rand.IntN(10) == 0 {
			return "c-town"
		}
		return fmt.Sprintf("c-load-%03d", 1+rand.IntN(c.channels))
	}
	for tick := 1; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-stats.C:
			logRuntimeMemory()
			continue
		case <-t.C:
		}
		i := rand.IntN(len(c.fakes))
		words := make([]string, 3+rand.IntN(40))
		for j := range words {
			words[j] = churnWords[rand.IntN(len(churnWords))]
		}
		c.fakes[i].PostAs(channel(), "bob", strings.Join(words, " ")) // bob is a member of every seeded channel
		if tick%churnSwitchEvery == 0 {
			j := rand.IntN(len(c.serverIDs))
			c.open(c.serverIDs[j], channel())
		}
	}
}

// logRuntimeMemory logs the Go heap figures a soak run compares over time.
func logRuntimeMemory() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	const mb = 1 << 20
	slog.Info("go memory", "heap_alloc_mb", m.HeapAlloc/mb, "heap_inuse_mb", m.HeapInuse/mb,
		"heap_idle_mb", m.HeapIdle/mb, "heap_released_mb", m.HeapReleased/mb, "next_gc_mb", m.NextGC/mb,
		"sys_mb", m.Sys/mb, "num_gc", m.NumGC)
}
