package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// The PDF memory check (PDF Task 4): a dev desktop run with --mm-fake
// --mm-fake-pdf-cycles N drives the real viewer (runPDFCycles) with nothing
// attached — N opens of a 50-page picture-heavy PDF — logs the web
// process's memory after every close and a trend summary, and quits. There
// is no pass/fail: the rule is "no unbounded growth" (the +20 MB gate of the
// plan was withdrawn by the user on 2026-09-29; after heavy use the web
// process sits +30…+40 MB over its pre-PDF baseline, bounded — AGENTS.md,
// spike doc S4 «PDF: гейт памяти»). Judge the closes in the log.
const (
	// pdfMarkPrefix: the page's answers to the driver's scripts arrive as
	// Wails raw messages with this prefix (window._wails.invoke).
	pdfMarkPrefix = "spk-dev-pdf:"
)

// pdfPace: the driver's timing. The defaults follow the spike's in-app runs
// (pdf-spike-report.md: 8 s reading, ~30 pages scrolled, 25 s after a close).
type pdfPace struct {
	settle        time.Duration // after start, before the channel with the PDF opens
	channelSettle time.Duration // after the channel opens, before the baseline
	baseline      time.Duration // sampled before the first open
	dwell         time.Duration // reading the first page, and again after scrolling
	gap           time.Duration // after a close; its lowest sample is the close's figure
	idle          time.Duration // after the last close: the level once WebKit has had time to give memory back (reported with the closes)
	markWait      time.Duration // the longest the page may take to answer a script
	tick          time.Duration // memory sampling interval
	scrollSteps   int
	scrollEvery   time.Duration
}

type pdfCycles struct {
	n           int
	openChannel func()               // shows the channel holding manual.pdf (the notification-click path)
	exec        func(js string)      // runs a script in the page
	marks       <-chan string        // the page's answers, without pdfMarkPrefix
	sample      func() (int64, bool) // the web process's Private_Dirty, kB
	pace        pdfPace
}

type pdfCycleResult struct {
	OpenPeakMB, ClosedMinMB, ClosedMaxMB float64
	Canvases                             string // canvases alive after scrolling, as the page reported
}

type pdfCheckResult struct {
	BaselineMB     float64
	IdleMinMB      float64
	Cycles         []pdfCycleResult
	MaxDeltaMB     float64
	SlopeMBPerOpen float64
}

// The scripts start with a comment naming them (the tests' fake page reads
// it). Each answers with one mark: "error …" on failure.
const pdfOpenJS = `/*open*/(async () => {
  const say = (m) => window._wails.invoke('` + pdfMarkPrefix + `' + m)
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
  try {
    const find = () => [...document.querySelectorAll('button[aria-label]')].find((b) => b.getAttribute('aria-label').includes('manual.pdf'))
    let b = find()
    for (let k = 0; k < 120 && !b; k++) { // the feed is virtualized: scroll up until the post is there
      const feed = document.querySelector('[role=log]')
      if (feed) feed.scrollTop -= 400
      await sleep(250)
      b = find()
    }
    if (!b) throw new Error('no preview button for manual.pdf')
    b.scrollIntoView({ block: 'center' })
    b.click()
    for (let k = 0; k < 240 && !document.querySelector('[role=dialog] canvas'); k++) await sleep(250)
    say(document.querySelector('[role=dialog] canvas') ? 'rendered' : 'error no page rendered')
  } catch (e) { say('error ' + e) }
})()`

func pdfScrollJS(steps int, every time.Duration) string {
	return fmt.Sprintf(`/*scroll*/(async () => {
  const say = (m) => window._wails.invoke('%s' + m)
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
  const sc = document.querySelector('[role=dialog] [data-page]')?.parentElement
  if (!sc) { say('error no pdf scroller'); return }
  for (let k = 0; k < %d; k++) { sc.scrollTop += 1200; await sleep(%d) }
  say('scrolled canvases=' + document.querySelectorAll('[role=dialog] canvas').length)
})()`, pdfMarkPrefix, steps, every.Milliseconds())
}

const pdfCloseJS = `/*close*/(async () => {
  const say = (m) => window._wails.invoke('` + pdfMarkPrefix + `' + m)
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  for (let k = 0; k < 40 && document.querySelector('[role=dialog]'); k++) await new Promise((r) => setTimeout(r, 250))
  say(document.querySelector('[role=dialog]') ? 'error the viewer did not close' : 'closed')
})()`

// runPDFCycles opens manual.pdf in the viewer c.n times — reads, scrolls
// ~30 pages, reads, closes, waits — sampling the web process all along, and
// logs each cycle and the verdict.
func runPDFCycles(ctx context.Context, c pdfCycles) (pdfCheckResult, error) {
	var res pdfCheckResult
	p := c.pace
	if !sleepCtx(ctx, p.settle) {
		return res, ctx.Err()
	}
	c.openChannel()
	if !sleepCtx(ctx, p.channelSettle) {
		return res, ctx.Err()
	}
	lo, _, ok := c.watch(ctx, p.baseline)
	if !ok {
		return res, errors.New("pdf memory check: the web process's memory is not readable")
	}
	res.BaselineMB = lo
	slog.Info("pdf memory baseline", "web_mb", round1(lo))
	closed := make([]float64, 0, c.n)
	for i := range c.n {
		var cy pdfCycleResult
		peak := 0.0
		step := func(js string, then time.Duration) (string, error) {
			c.exec(js)
			mark, hi, err := c.await(ctx)
			peak = max(peak, hi)
			if err != nil {
				return "", fmt.Errorf("pdf memory check: cycle %d: %w", i+1, err)
			}
			_, hi, _ = c.watch(ctx, then)
			peak = max(peak, hi)
			return mark, ctx.Err()
		}
		if _, err := step(pdfOpenJS, p.dwell); err != nil {
			return res, err
		}
		mark, err := step(pdfScrollJS(p.scrollSteps, p.scrollEvery), p.dwell)
		if err != nil {
			return res, err
		}
		cy.Canvases = strings.TrimPrefix(mark, "scrolled canvases=")
		c.exec(pdfCloseJS)
		if _, _, err := c.await(ctx); err != nil {
			return res, fmt.Errorf("pdf memory check: cycle %d: %w", i+1, err)
		}
		cy.OpenPeakMB = peak
		cy.ClosedMinMB, cy.ClosedMaxMB, _ = c.watch(ctx, p.gap)
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Cycles = append(res.Cycles, cy)
		closed = append(closed, cy.ClosedMinMB)
		slog.Info("pdf memory cycle", "n", i+1, "open_peak_mb", round1(cy.OpenPeakMB), "closed_min_mb", round1(cy.ClosedMinMB),
			"closed_max_mb", round1(cy.ClosedMaxMB), "delta_mb", round1(cy.ClosedMinMB-lo), "canvases", cy.Canvases)
	}
	res.IdleMinMB, _, _ = c.watch(ctx, p.idle)
	res.MaxDeltaMB, res.SlopeMBPerOpen = pdfTrend(lo, closed)
	deltas := make([]string, len(closed))
	for i, v := range closed {
		deltas[i] = strconv.FormatFloat(round1(v-lo), 'f', 1, 64)
	}
	slog.Info("pdf memory result", "baseline_mb", round1(lo), "max_delta_mb", round1(res.MaxDeltaMB), "idle_delta_mb", round1(res.IdleMinMB-lo),
		"slope_mb_per_open", math.Round(res.SlopeMBPerOpen*100)/100, "deltas_mb", strings.Join(deltas, " "))
	return res, nil
}

// await: the page's next answer, sampling meanwhile; its peak in MB.
func (c pdfCycles) await(ctx context.Context) (string, float64, error) {
	t := time.NewTicker(c.pace.tick)
	defer t.Stop()
	deadline := time.NewTimer(c.pace.markWait)
	defer deadline.Stop()
	peak := 0.0
	for {
		select {
		case <-ctx.Done():
			return "", peak, ctx.Err()
		case <-deadline.C:
			return "", peak, errors.New("no answer from the page")
		case m := <-c.marks:
			if msg, bad := strings.CutPrefix(m, "error"); bad {
				return m, peak, errors.New(strings.TrimSpace(msg))
			}
			return m, peak, nil
		case <-t.C:
			if kb, ok := c.sample(); ok {
				peak = max(peak, float64(kb)/1024)
			}
		}
	}
}

// watch samples for d: the lowest and highest Private_Dirty in MB.
func (c pdfCycles) watch(ctx context.Context, d time.Duration) (lo, hi float64, ok bool) {
	lo = math.Inf(1)
	end := time.Now().Add(d)
	for {
		if kb, got := c.sample(); got {
			v := float64(kb) / 1024
			lo, hi, ok = min(lo, v), max(hi, v), true
		}
		if time.Now().After(end) || !sleepCtx(ctx, c.pace.tick) {
			break
		}
	}
	if !ok {
		lo = 0
	}
	return lo, hi, ok
}

// pdfTrend: the largest close over the baseline and the least-squares slope
// of the closes (MB per open) — numbers to judge, not a verdict.
func pdfTrend(baseline float64, closed []float64) (maxDelta, slope float64) {
	n := float64(len(closed))
	var sx, sy, sxx, sxy float64
	maxDelta = math.Inf(-1)
	for i, v := range closed {
		x := float64(i)
		sx, sy, sxx, sxy = sx+x, sy+v, sxx+x*x, sxy+x*v
		maxDelta = max(maxDelta, v-baseline)
	}
	if d := n*sxx - sx*sx; d != 0 {
		slope = (n*sxy - sx*sy) / d
	}
	return maxDelta, slope
}

// pdfMarkFilter: the handler for the page's raw messages — only the
// driver's own (pdfMarkPrefix) go to marks, without blocking the caller
// (Wails' message loop).
func pdfMarkFilter(marks chan<- string) func(string) {
	return func(m string) {
		if mark, ok := strings.CutPrefix(m, pdfMarkPrefix); ok {
			select {
			case marks <- mark:
			default:
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// findDescendant: the first descendant of pid whose comm is name (Linux /proc).
func findDescendant(pid int, name string) (int, bool) {
	queue := []int{pid}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		tasks, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", p))
		for _, task := range tasks {
			b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", p, task.Name()))
			for _, f := range strings.Fields(string(b)) {
				child, err := strconv.Atoi(f)
				if err != nil {
					continue
				}
				if comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", child)); strings.TrimSpace(string(comm)) == name {
					return child, true
				}
				queue = append(queue, child)
			}
		}
	}
	return 0, false
}

func privateDirtyKB(pid int) (int64, bool) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	return parsePrivateDirty(f)
}

func parsePrivateDirty(r io.Reader) (int64, bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "Private_Dirty:"); ok {
			v, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			return v, err == nil
		}
	}
	return 0, false
}

// checkDevFlags: the fake server exists only in dev builds, and the PDF
// driver only with it.
func checkDevFlags(o desktopOpts, devBuild bool) error {
	if (o.MMFake || o.FakePDFCycles > 0) && !devBuild {
		return errors.New("--mm-fake is available in development builds only")
	}
	if o.FakePDFCycles > 0 && !o.MMFake {
		return errors.New("--mm-fake-pdf-cycles needs --mm-fake")
	}
	return nil
}
