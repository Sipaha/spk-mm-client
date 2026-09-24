//go:build wails

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/appfiles"
	"github.com/spk/spk-mattermost/internal/desktop"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
)

func runDesktop(ctx context.Context) error {
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
		slog.Error("sync did not start; chats stay offline", "err", err) // never block the window on it
	}
	defer svc.Close()
	return desktop.Run(ctx, desktop.Options{
		FrontendFS: frontendFS(),
		Service:    svc,
		Emitter:    em,
		IconPNG:    appfiles.IconPNG,
	})
}
