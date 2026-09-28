package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

// fakeChurn drives a dev desktop run for memory soak tests
// (--mm-fake-churn): every tick bob posts to a random channel
// of a random fake server — every third post a reply to one of the
// channel's recent roots —, and every few ticks the UI is asked to open a
// random channel (the same path a notification click takes; every fifth
// time a reply's notification: the channel and a thread in the panel), so
// the feed, markdown rendering, channel switching, the thread panel and the
// thread cache all keep running for hours. Posts carry no @mention: the
// soak must not flood the desktop with notifications.
type fakeChurn struct {
	every     time.Duration
	fakes     []*mmfake.Server
	serverIDs []int64 // server id of fakes[i] in the service
	channels  int     // extra "c-load-NNN" channels per fake
	open      func(serverID int64, channelID, rootID string)
}

const (
	churnSwitchEvery = 5 // ticks between channel switches
	churnReplyEvery  = 3 // every third post is a reply
	churnThreadEvery = 5 // every fifth switch opens a thread
	churnRecentRoots = 5 // a reply or a thread opening picks one of this many newest roots
)

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
	profDir := os.Getenv("SPK_MM_CLIENT_SOAK_PROFILES")
	if profDir != "" {
		runtime.MemProfileRate = 64 << 10
	}
	minute := 0
	for tick := 1; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-stats.C:
			logRuntimeMemory()
			minute++
			if profDir != "" {
				writeHeapProfile(filepath.Join(profDir, fmt.Sprintf("heap-%03d.pb.gz", minute)))
			}
			continue
		case <-t.C:
		}
		i := rand.IntN(len(c.fakes))
		words := make([]string, 3+rand.IntN(40))
		for j := range words {
			words[j] = churnWords[rand.IntN(len(churnWords))]
		}
		ch, root := channel(), ""
		if tick%churnReplyEvery == 0 {
			root = recentRoot(c.fakes[i], ch)
		}
		c.fakes[i].ReplyAs(ch, root, "bob", strings.Join(words, " ")) // bob is a member of every seeded channel
		if tick%churnSwitchEvery == 0 {
			j := rand.IntN(len(c.serverIDs))
			ch, root := channel(), ""
			if (tick/churnSwitchEvery)%churnThreadEvery == 0 {
				root = recentRoot(c.fakes[j], ch)
			}
			c.open(c.serverIDs[j], ch, root)
		}
	}
}

// recentRoot is one of the newest churnRecentRoots user roots of a channel
// ("" if it has none). Only the churn posts to a fake while it runs, so the
// root is still there (not trimmed by KeepPosts) when the reply lands.
func recentRoot(f *mmfake.Server, channelID string) string {
	posts := f.VisiblePosts(channelID)
	roots := make([]string, 0, churnRecentRoots)
	for i := len(posts) - 1; i >= 0 && len(roots) < churnRecentRoots; i-- {
		if p := posts[i]; p.RootID == "" && p.Type == "" {
			roots = append(roots, p.ID)
		}
	}
	if len(roots) == 0 {
		return ""
	}
	return roots[rand.IntN(len(roots))]
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

// writeHeapProfile saves the heap profile (as of the last GC) for a soak
// run's `go tool pprof -base` diffs.
func writeHeapProfile(path string) {
	f, err := os.Create(path)
	if err != nil {
		slog.Warn("heap profile", "err", err)
		return
	}
	defer f.Close()
	if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
		slog.Warn("heap profile", "err", err)
	}
}
