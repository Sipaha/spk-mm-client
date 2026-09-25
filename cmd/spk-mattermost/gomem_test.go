package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func restoreGoMemory(t *testing.T) {
	gc := debug.SetGCPercent(100)
	limit := debug.SetMemoryLimit(-1)
	debug.SetGCPercent(gc)
	t.Cleanup(func() {
		debug.SetGCPercent(gc)
		debug.SetMemoryLimit(limit)
	})
}

func currentGCPercent() int {
	v := debug.SetGCPercent(100)
	debug.SetGCPercent(v)
	return v
}

// The app sets its own GC target and soft limit unless the user chose them.
func TestTuneGoMemoryDefaults(t *testing.T) {
	restoreGoMemory(t)
	tuneGoMemory(func(string) string { return "" })
	assert.Equal(t, goGCPercent, currentGCPercent())
	assert.Equal(t, int64(goMemoryLimit), debug.SetMemoryLimit(-1))
}

func TestTuneGoMemoryRespectsEnvironment(t *testing.T) {
	restoreGoMemory(t)
	debug.SetGCPercent(100)
	debug.SetMemoryLimit(1 << 40)
	env := map[string]string{"GOGC": "100", "GOMEMLIMIT": "1TiB"}
	tuneGoMemory(func(k string) string { return env[k] })
	assert.Equal(t, 100, currentGCPercent())
	assert.Equal(t, int64(1<<40), debug.SetMemoryLimit(-1))
}
