package main

import (
	"context"
	"net"
	"net/url"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/mmfake"
)

// signInToFake adds the in-process fake server and signs alice in, so a dev
// desktop run shows a working chat at once. The fake listens on a new port
// every run, so fake servers left in the store by earlier runs (same home)
// point at dead ports: they are removed rather than piling up.
func signInToFake(ctx context.Context, svc *api.Service, fakeURL string) error {
	list, err := svc.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, s := range list {
		if isDevFake(s) && s.URL != fakeURL {
			if err := svc.RemoveServer(ctx, s.ID); err != nil {
				return err
			}
		}
	}
	srv, err := svc.AddServer(ctx, fakeURL)
	if err != nil {
		return err
	}
	_, err = svc.LoginWithPassword(ctx, srv.ID, "alice", "secret")
	return err
}

// isDevFake: an in-process fake from a dev run (loopback URL, fake site name).
func isDevFake(s api.ServerDTO) bool {
	u, err := url.Parse(s.URL)
	if err != nil || s.Name != mmfake.DefaultSiteName {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
