package main

import (
	"context"
	"net"
	"net/url"
	"slices"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/mmfake"
)

// signInToFake adds the in-process fake servers and signs alice in on each,
// so a dev desktop run shows a working chat at once; it returns their server
// ids in the order of fakeURLs. The fakes listen on new ports every run, so
// fake servers left in the store by earlier runs (same home) point at dead
// ports: they are removed rather than piling up.
func signInToFake(ctx context.Context, svc *api.Service, fakeURLs ...string) ([]int64, error) {
	list, err := svc.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if isDevFake(s) && !slices.Contains(fakeURLs, s.URL) {
			if err := svc.RemoveServer(ctx, s.ID); err != nil {
				return nil, err
			}
		}
	}
	ids := make([]int64, 0, len(fakeURLs))
	for _, u := range fakeURLs {
		srv, err := svc.AddServer(ctx, u)
		if err != nil {
			return nil, err
		}
		if _, err := svc.LoginWithPassword(ctx, srv.ID, "alice", "secret"); err != nil {
			return nil, err
		}
		ids = append(ids, srv.ID)
	}
	return ids, nil
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
