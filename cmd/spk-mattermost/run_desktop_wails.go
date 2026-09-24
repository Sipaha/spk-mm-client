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

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/appfiles"
	"github.com/spk/spk-mattermost/internal/desktop"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
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
	if err := svc.Start(ctx); err != nil {
		slog.Error("sync did not start; chats stay offline", "err", err) // never keep the window from opening
	}
	defer svc.Close()

	var dev []desktop.DevAction
	if o.MMFake {
		fake := mmfake.Start(mmfake.Options{ExtraChannels: o.FakeChannels})
		defer fake.Close()
		slog.Warn("fake Mattermost server started (development only)", "url", fake.URL())
		if err := signInToFake(ctx, svc, fake.URL()); err != nil {
			return fmt.Errorf("fake server sign-in: %w", err)
		}
		dev = append(dev, desktop.DevAction{Label: "Fake: mention from bob", Run: func() {
			fake.PostAs("c-offtopic", "bob", "@alice ping "+time.Now().Format("15:04:05"))
		}})
	}

	return desktop.Run(ctx, desktop.Options{
		FrontendFS:     frontendFS(),
		Service:        svc,
		Emitter:        em,
		IconPNG:        appfiles.IconPNG,
		IconUnreadPNG:  appfiles.IconUnreadPNG,
		IconMentionPNG: appfiles.IconMentionPNG,
		DevActions:     dev,
	})
}
