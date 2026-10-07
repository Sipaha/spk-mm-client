package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// version is supplied by the build; direct go builds honestly report dev.
var version = "dev"

type browserOpts struct {
	Port    int
	MMFake  bool
	TestAPI bool
	// FakeChannels adds open channels to the fake server (memory checks).
	FakeChannels int
}

type desktopOpts struct {
	MMFake       bool // dev builds only: in-process fake server, signed in as alice
	FakeChannels int
	FakeServers  int           // dev builds only: how many fake servers (memory checks with 2–3 servers)
	FakeChurn    time.Duration // dev builds only: post/switch-channel interval for soak runs, 0 = off
	// FakePDFCycles: dev builds, with MMFake: open/scroll/close the seeded
	// manual.pdf this many times, log the web process's memory and quit
	// (the PDF memory check, devpdf.go). 0 = off.
	FakePDFCycles int
}

type runners struct {
	browser func(ctx context.Context, o browserOpts) error
	desktop func(ctx context.Context, o desktopOpts) error
}

func newRootCmd(run runners) *cobra.Command {
	var o browserOpts
	var browser bool
	var fakeServers int
	var fakeChurn time.Duration
	var fakePDFCycles int
	root := &cobra.Command{
		Use:           "spk-mm-client [mmauth://callback?...]",
		Short:         "Lightweight Mattermost desktop client",
		SilenceUsage:  true,
		SilenceErrors: true,
		// The OS hands a deep link (mmauth://...) over as a positional arg;
		// Wails reads it from os.Args itself, cobra just has to accept it.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if browser {
				return run.browser(cmd.Context(), o)
			}
			return run.desktop(cmd.Context(), desktopOpts{
				MMFake: o.MMFake, FakeChannels: o.FakeChannels, FakeServers: fakeServers, FakeChurn: fakeChurn, FakePDFCycles: fakePDFCycles,
			})
		},
	}
	root.Flags().BoolVar(&browser, "browser", false, "Serve the UI over HTTP on localhost instead of opening a window")
	root.Flags().IntVar(&o.Port, "port", 5180, "HTTP port for --browser")
	root.Flags().BoolVar(&o.MMFake, "mm-fake", false, "Start an in-process fake Mattermost server (development/e2e only; desktop: dev builds, signs in as alice)")
	root.Flags().IntVar(&o.FakeChannels, "mm-fake-channels", 0, "Extra open channels (20 posts each) in the fake server — memory checks")
	root.Flags().IntVar(&fakeServers, "mm-fake-servers", 1, "Desktop --mm-fake: number of fake servers, each with the same seed (memory checks)")
	root.Flags().DurationVar(&fakeChurn, "mm-fake-churn", 0, "Desktop --mm-fake: post to a random channel every interval and switch channels every 5th (soak runs)")
	root.Flags().IntVar(&fakePDFCycles, "mm-fake-pdf-cycles", 0, "Desktop --mm-fake: open and close the fake manual.pdf N times, log the web process's memory, quit (PDF memory check)")
	root.Flags().BoolVar(&o.TestAPI, "test-api", false, "Expose /api/_test/* automation routes (development/e2e only)")
	return root
}

func main() {
	tuneGoMemory(os.Getenv)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := newRootCmd(runners{browser: runBrowser, desktop: runDesktop})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
