package main

import "runtime/debug"

// Go runtime memory policy (docs/spikes/2026-09-24-stage1-spikes.md, S4,
// «Перерасход на живом клиенте»). The client's live Go heap is small (~5–10 MB),
// so the default GOGC=100 lets the heap — and the RSS the runtime keeps —
// reach about twice that between collections; that headroom, not live data,
// was most of the main process's Go heap. GOGC=50 trades a few more
// sub-millisecond collections for ~4 MB less Private_Dirty. The soft limit
// is a ceiling for spikes (a large server's bootstrap): in soak runs with
// 3 servers × 100 channels, the live heap stayed around 20 MB, so the limit
// has ~3× headroom. If the live heap approaches 64 MiB, the Go runtime
// collects more often; at limit, it caps GC CPU at ~50% to avoid excessive
// overhead, so the app slows down rather than crashes. Escape hatch: set
// GOMEMLIMIT or GOGC in the environment (these override defaults). Tuning
// runs before mode dispatch, so it applies to both desktop and --browser modes.
const (
	goGCPercent   = 50
	goMemoryLimit = 64 << 20
)

func tuneGoMemory(getenv func(string) string) {
	if getenv("GOGC") == "" {
		debug.SetGCPercent(goGCPercent)
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(goMemoryLimit)
	}
}
