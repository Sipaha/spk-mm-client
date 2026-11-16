package desktop

import (
	"context"
	"time"

	"github.com/spk/spk-mm-client/internal/api"
)

type trayApplier struct {
	waitReady func(context.Context) bool
	setText   func(string)
	setIcon   func(trayIcon)
}

// waitUntilReady polls probe until it reports true or ctx is cancelled. Once
// warnAfter elapses without readiness, onWarn is called once and polling slows
// from fastPoll to slowPoll so delayed desktop/D-Bus startup never terminates
// the badge consumer permanently.
func waitUntilReady(
	ctx context.Context,
	warnAfter, fastPoll, slowPoll time.Duration,
	probe func(context.Context) bool,
	onWarn func(),
) bool {
	start := time.Now()
	warned := false
	for {
		if ctx.Err() != nil {
			return false
		}
		if probe(ctx) {
			return ctx.Err() == nil
		}
		if !warned && time.Since(start) >= warnAfter {
			warned = true
			if onWarn != nil {
				onWarn()
			}
		}
		wait := fastPoll
		if warned {
			wait = slowPoll
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
}

// runTrayBadge consumes coalesced badge updates from ch once waitReady reports
// the native tray is ready. Hover text updates whenever it changes; the PNG
// icon is decoded and pushed on the initial update and whenever the icon state
// (plain/unread/mention) transitions.
func runTrayBadge(ctx context.Context, ch <-chan api.Badge, lang string, a trayApplier) {
	if a.waitReady != nil && !a.waitReady(ctx) {
		return
	}
	var (
		haveText bool
		lastText string
		haveIcon bool
		lastIcon trayIcon
	)
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-ch:
			if ctx.Err() != nil {
				return
			}
			text := trayTooltip(lang, b.Unread, b.Mentions)
			if !haveText || text != lastText {
				a.setText(text)
				lastText, haveText = text, true
			}
			ic := trayIconFor(b.Unread, b.Mentions)
			if !haveIcon || ic != lastIcon {
				a.setIcon(ic)
				lastIcon, haveIcon = ic, true
			}
		}
	}
}
