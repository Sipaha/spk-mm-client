package desktop

import (
	"context"
	"time"
)

// startWithTimeout runs start in a background goroutine bounded by timeout
// and reports whether it completed successfully within that bound. It is
// the decision logic behind notifier.ServiceStartup (internal/desktop/
// notify.go, wails-tagged and so untestable directly without a display):
// kept here, untagged, so it has a plain unit test.
//
// available is true only if start returned within timeout with a nil error.
// err is start's error when it finished in time and failed; on timeout err
// is nil — a timeout is "didn't show up in time", not a reported failure,
// so callers distinguish "log the error" from "log a timeout" using err.
//
// start's goroutine is not cancelled on timeout: many blocking startup
// calls (e.g. dbus.ConnectSessionBus) are not context-aware. A timed-out
// start may still finish afterward; its result is simply discarded, since
// the caller has already decided to treat the resource as unavailable.
func startWithTimeout(ctx context.Context, start func(context.Context) error, timeout time.Duration) (available bool, err error) {
	done := make(chan error, 1)
	go func() {
		done <- start(ctx)
	}()
	select {
	case e := <-done:
		return e == nil, e
	case <-time.After(timeout):
		return false, nil
	}
}
