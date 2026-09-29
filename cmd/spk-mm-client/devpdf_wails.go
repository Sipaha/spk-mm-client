//go:build wails

package main

import (
	"os"
	"time"
)

// The dev desktop run's side of the PDF memory check (devpdf.go).

var defaultPDFPace = pdfPace{
	settle: 20 * time.Second, channelSettle: 10 * time.Second, baseline: 15 * time.Second, dwell: 8 * time.Second, gap: 25 * time.Second, idle: 2 * time.Minute,
	markWait: 90 * time.Second, tick: time.Second, scrollSteps: 30, scrollEvery: 200 * time.Millisecond,
}

// webProcessSampler: the Private_Dirty of the app's WebKitWebProcess (the
// memory budget's metric, AGENTS.md), found among this process's
// descendants on every call — WebKit may restart it.
func webProcessSampler() func() (int64, bool) {
	return func() (int64, bool) {
		pid, ok := findDescendant(os.Getpid(), "WebKitWebProces") // comm is cut to 15 bytes
		if !ok {
			return 0, false
		}
		return privateDirtyKB(pid)
	}
}
