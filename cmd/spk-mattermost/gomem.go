package main

import "runtime/debug"

// Go runtime memory policy (docs/spikes/2026-09-24-stage1-spikes.md, S4
// "memory budget overrun"). The client's live Go heap is small (~5–10 MB),
// so the default GOGC=100 lets the heap — and the RSS the runtime keeps —
// reach about twice that between collections; that headroom, not live data,
// was most of the main process's Go heap. GOGC=50 trades a few more
// sub-millisecond collections for ~4 MB less Private_Dirty. The soft limit
// is a ceiling for spikes (a large server's bootstrap): near it the GC runs
// more often and the scavenger returns freed pages to the OS promptly; it is
// far above the steady state, so it never binds in normal use. GOGC and
// GOMEMLIMIT from the environment still win.
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
