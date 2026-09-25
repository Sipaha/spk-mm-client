//go:build wails

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/appfiles"
	"github.com/spk/spk-mm-client/internal/desktop"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/paths"
	"github.com/spk/spk-mm-client/internal/store"
)

func runDesktop(ctx context.Context, o desktopOpts) error {
	if o.MMFake && !desktop.DevBuild {
		return errors.New("--mm-fake is available in development builds only")
	}
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	em := events.NewEmitter()
	open := func(u string) error { return application.Get().Browser.OpenURL(u) }
	svc := api.NewService(st, em, open, &http.Client{Timeout: 30 * time.Second})
	// xdg-open (Wails Browser.OpenFile) starts and returns at once.
	svc.SetFileOpener(func(path string) error { return application.Get().Browser.OpenFile(path) })
	if err := svc.Start(ctx); err != nil {
		slog.Error("sync did not start; chats stay offline", "err", err) // never keep the window from opening
	}
	defer svc.Close()

	// A nil *media.Cache in the interface would not be a nil handler:
	// mediaH stays a nil interface when the cache cannot be opened.
	var mediaH http.Handler
	if mc, err := media.New(media.Options{Dir: p.MediaDir, Origin: svc}); err != nil {
		slog.Warn("media cache unavailable; pictures will not load", "err", err)
	} else {
		mediaH = mc
	}

	var dev []desktop.DevAction
	if o.MMFake {
		fakes := make([]*mmfake.Server, max(o.FakeServers, 1))
		urls := make([]string, len(fakes))
		for i := range fakes {
			fakes[i] = mmfake.Start(mmfake.Options{ExtraChannels: o.FakeChannels})
			defer fakes[i].Close()
			urls[i] = fakes[i].URL()
			slog.Warn("fake Mattermost server started (development only)", "url", urls[i])
		}
		ids, err := signInToFake(ctx, svc, urls...)
		if err != nil {
			return fmt.Errorf("fake server sign-in: %w", err)
		}
		fake := fakes[0]
		dev = append(dev, desktop.DevAction{Label: "Fake: mention from bob", Run: func() {
			fake.PostAs("c-offtopic", "bob", "@alice ping "+time.Now().Format("15:04:05"))
		}})
		if o.FakeChurn > 0 {
			churnCtx, stopChurn := context.WithCancel(ctx)
			defer stopChurn() // before the fakes close (defers run in reverse)
			go runFakeChurn(churnCtx, fakeChurn{
				every: o.FakeChurn, fakes: fakes, serverIDs: ids, channels: o.FakeChannels,
				open: svc.NotificationClicked,
			})
		}
	}

	return desktop.Run(ctx, desktop.Options{
		FrontendFS:     frontendFS(),
		Service:        svc,
		Emitter:        em,
		IconPNG:        appfiles.IconPNG,
		IconUnreadPNG:  appfiles.IconUnreadPNG,
		IconMentionPNG: appfiles.IconMentionPNG,
		DevActions:     dev,
		Media:          mediaH,
	})
}
