package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

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
}

type runners struct {
	browser func(ctx context.Context, o browserOpts) error
	desktop func(ctx context.Context, o desktopOpts) error
}

func newRootCmd(run runners) *cobra.Command {
	var o browserOpts
	var browser bool
	root := &cobra.Command{
		Use:           "spk-mattermost [mmauth://callback?...]",
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
			return run.desktop(cmd.Context(), desktopOpts{MMFake: o.MMFake, FakeChannels: o.FakeChannels})
		},
	}
	root.Flags().BoolVar(&browser, "browser", false, "Serve the UI over HTTP on localhost instead of opening a window")
	root.Flags().IntVar(&o.Port, "port", 5180, "HTTP port for --browser")
	root.Flags().BoolVar(&o.MMFake, "mm-fake", false, "Start an in-process fake Mattermost server (development/e2e only; desktop: dev builds, signs in as alice)")
	root.Flags().IntVar(&o.FakeChannels, "mm-fake-channels", 0, "Extra open channels (20 posts each) in the fake server — memory checks")
	root.Flags().BoolVar(&o.TestAPI, "test-api", false, "Expose /api/_test/* automation routes (development/e2e only)")
	return root
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := newRootCmd(runners{browser: runBrowser, desktop: runDesktop})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
