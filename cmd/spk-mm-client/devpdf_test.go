package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDesktopPDFCyclesFlag(t *testing.T) {
	var got desktopOpts
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { t.Fatal("browser runner called"); return nil },
		desktop: func(_ context.Context, o desktopOpts) error { got = o; return nil },
	})
	cmd.SetArgs([]string{"--mm-fake", "--mm-fake-pdf-cycles", "20"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.Equal(t, desktopOpts{MMFake: true, FakeServers: 1, FakePDFCycles: 20}, got)
}

// The PDF driver runs only against the in-process fake, which only dev
// builds have: a release build refuses both flags instead of ignoring them.
func TestDevFlagsNeedADevBuildAndTheFake(t *testing.T) {
	assert.NoError(t, checkDevFlags(desktopOpts{}, false))
	assert.NoError(t, checkDevFlags(desktopOpts{MMFake: true, FakePDFCycles: 3}, true))
	assert.ErrorContains(t, checkDevFlags(desktopOpts{MMFake: true}, false), "development builds only")
	assert.ErrorContains(t, checkDevFlags(desktopOpts{FakePDFCycles: 3}, true), "--mm-fake")
	assert.ErrorContains(t, checkDevFlags(desktopOpts{FakePDFCycles: 3}, false), "development builds only")
}

// fakePage answers the driver's scripts the way the app's page does: each
// script says what it is in a leading comment, and the page posts a mark
// back (through window._wails.invoke in the real app).
type fakePage struct {
	mu      sync.Mutex
	scripts []string
	marks   chan string
	reply   func(kind string) string
}

func (p *fakePage) exec(js string) {
	kind := strings.TrimSuffix(strings.TrimPrefix(js[:strings.Index(js, "*/")+2], "/*"), "*/")
	p.mu.Lock()
	p.scripts = append(p.scripts, kind)
	p.mu.Unlock()
	go func() { p.marks <- p.reply(kind) }()
}

func (p *fakePage) kinds() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.scripts...)
}

func quickPace() pdfPace {
	return pdfPace{settle: time.Millisecond, channelSettle: 30 * time.Millisecond, baseline: 20 * time.Millisecond, dwell: 5 * time.Millisecond,
		gap: 20 * time.Millisecond, idle: 20 * time.Millisecond, markWait: time.Second, tick: time.Millisecond, scrollSteps: 3, scrollEvery: time.Millisecond}
}

func TestPDFCyclesOpenScrollAndCloseTheViewer(t *testing.T) {
	page := &fakePage{marks: make(chan string, 4), reply: func(kind string) string {
		switch kind {
		case "open":
			return "rendered"
		case "scroll":
			return "scrolled canvases=3"
		}
		return "closed"
	}}
	var mu sync.Mutex
	opened, open := 0, false
	res, err := runPDFCycles(context.Background(), pdfCycles{
		n:           3,
		openChannel: func() { mu.Lock(); opened++; mu.Unlock() },
		exec: func(js string) {
			mu.Lock()
			open = strings.HasPrefix(js, "/*open*/") || (open && !strings.HasPrefix(js, "/*close*/"))
			mu.Unlock()
			page.exec(js)
		},
		marks: page.marks,
		sample: func() (int64, bool) { // 100 MB idle, 180 MB with the viewer open, 105 MB after
			mu.Lock()
			defer mu.Unlock()
			switch {
			case open:
				return 180 << 10, true
			case len(page.kinds()) > 0:
				return 105 << 10, true
			}
			return 100 << 10, true
		},
		pace: quickPace(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, opened, "the channel with the PDF is opened once, before the baseline")
	assert.Equal(t, []string{"open", "scroll", "close", "open", "scroll", "close", "open", "scroll", "close"}, page.kinds())
	assert.InDelta(t, 100, res.BaselineMB, 0.01)
	require.Len(t, res.Cycles, 3)
	for _, c := range res.Cycles {
		assert.InDelta(t, 180, c.OpenPeakMB, 0.01)
		assert.InDelta(t, 105, c.ClosedMinMB, 0.01)
	}
	assert.InDelta(t, 5, res.MaxDeltaMB, 0.01)
	assert.InDelta(t, 105, res.IdleMinMB, 0.01, "and the level after a longer idle, for the record")
	assert.InDelta(t, 0, res.SlopeMBPerOpen, 0.01)
}

func TestPDFCyclesStopOnAPageError(t *testing.T) {
	page := &fakePage{marks: make(chan string, 4), reply: func(string) string { return "error no preview button" }}
	_, err := runPDFCycles(context.Background(), pdfCycles{
		n: 5, openChannel: func() {}, exec: page.exec, marks: page.marks,
		sample: func() (int64, bool) { return 100 << 10, true }, pace: quickPace(),
	})
	assert.ErrorContains(t, err, "no preview button")
	assert.Equal(t, []string{"open"}, page.kinds())
}

func TestPDFCyclesNeedTheWebProcess(t *testing.T) {
	page := &fakePage{marks: make(chan string, 4), reply: func(string) string { return "rendered" }}
	_, err := runPDFCycles(context.Background(), pdfCycles{
		n: 1, openChannel: func() {}, exec: page.exec, marks: page.marks,
		sample: func() (int64, bool) { return 0, false }, pace: quickPace(),
	})
	assert.ErrorContains(t, err, "web process")
	assert.Empty(t, page.kinds())
}

func TestPDFCyclesGiveUpOnASilentPage(t *testing.T) {
	pace := quickPace()
	pace.markWait = 20 * time.Millisecond
	_, err := runPDFCycles(context.Background(), pdfCycles{
		n: 1, openChannel: func() {}, exec: func(string) {}, marks: make(chan string),
		sample: func() (int64, bool) { return 100 << 10, true }, pace: pace,
	})
	assert.ErrorContains(t, err, "no answer")
}

// The summary is raw numbers, no verdict (the +20 MB gate was withdrawn,
// user decision 2026-09-29 — the rule is "no unbounded growth"): the
// largest close over the baseline and the least-squares slope of the closes.
func TestPDFTrend(t *testing.T) {
	flat := []float64{112, 108, 115, 110, 109, 114, 111, 107, 113, 110}
	maxDelta, slope := pdfTrend(100, flat)
	assert.InDelta(t, 15, maxDelta, 0.01)
	assert.InDelta(t, 0, slope, 0.3)

	rising := make([]float64, 20)
	for i := range rising {
		rising[i] = 101 + float64(i)*0.9
	}
	maxDelta, slope = pdfTrend(100, rising)
	assert.InDelta(t, 18.1, maxDelta, 0.01)
	assert.InDelta(t, 0.9, slope, 0.01)
}

// A cancelled run (the app quitting) stops at once, touching no page.
func TestPDFCyclesStopWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pace := quickPace()
	pace.settle = time.Minute
	page := &fakePage{marks: make(chan string, 4), reply: func(string) string { return "rendered" }}
	start := time.Now()
	_, err := runPDFCycles(ctx, pdfCycles{
		n: 1, openChannel: func() { t.Error("channel opened") }, exec: page.exec, marks: page.marks,
		sample: func() (int64, bool) { return 100 << 10, true }, pace: pace,
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
	assert.Empty(t, page.kinds())
}

// The page's answers reach the driver only with the driver's prefix.
func TestPDFMarksFilter(t *testing.T) {
	marks := make(chan string, 4)
	f := pdfMarkFilter(marks)
	f("something else")
	f(pdfMarkPrefix + "rendered")
	require.Len(t, marks, 1)
	assert.Equal(t, "rendered", <-marks)
}

func TestParsePrivateDirty(t *testing.T) {
	const rollup = `55d0c0a3e000-7ffd5b5f6000 ---p 00000000 00:00 0                          [rollup]
Rss:              123456 kB
Pss:              100000 kB
Private_Clean:      1000 kB
Private_Dirty:     81234 kB
Swap:                  0 kB
`
	kb, ok := parsePrivateDirty(strings.NewReader(rollup))
	assert.True(t, ok)
	assert.Equal(t, int64(81234), kb)
	_, ok = parsePrivateDirty(strings.NewReader("Rss: 1 kB\n"))
	assert.False(t, ok)
}

// The web process is a child of the app (WebKitGTK spawns it): the driver
// finds it among its own descendants by name.
func TestFindsADescendantByName(t *testing.T) {
	if _, err := os.Stat("/proc/self/task"); err != nil {
		t.Skip("no /proc")
	}
	cmd := exec.Command("sleep", "5")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.Eventually(t, func() bool {
		pid, ok := findDescendant(os.Getpid(), "sleep")
		return ok && pid == cmd.Process.Pid
	}, 2*time.Second, 10*time.Millisecond)
	_, ok := findDescendant(os.Getpid(), "no-such-process")
	assert.False(t, ok)
	kb, ok := privateDirtyKB(os.Getpid())
	assert.True(t, ok)
	assert.Positive(t, kb)
}

// The baseline is the channel with the PDF on screen, once it has settled —
// not the app before the channel's pictures loaded (the first smoke run
// measured 56 MB instead of ~73 and read +17 MB too much).
func TestPDFBaselineWaitsForTheChannel(t *testing.T) {
	page := &fakePage{marks: make(chan string, 4), reply: func(kind string) string {
		return map[string]string{"open": "rendered", "scroll": "scrolled canvases=3", "close": "closed"}[kind]
	}}
	var mu sync.Mutex
	var openedAt time.Time
	res, err := runPDFCycles(context.Background(), pdfCycles{
		n: 1, exec: page.exec, marks: page.marks, pace: quickPace(),
		openChannel: func() { mu.Lock(); openedAt = time.Now(); mu.Unlock() },
		sample: func() (int64, bool) {
			mu.Lock()
			defer mu.Unlock()
			if openedAt.IsZero() || time.Since(openedAt) < 10*time.Millisecond {
				return 50 << 10, true // the channel's pictures are not in yet
			}
			return 100 << 10, true
		},
	})
	require.NoError(t, err)
	assert.InDelta(t, 100, res.BaselineMB, 0.01)
}
