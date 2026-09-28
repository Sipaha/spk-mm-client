package main

import (
	"context"
	"net"
	"net/url"
	"slices"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
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

// fakeOptions: the options of each in-process fake. A churn (soak) run seeds
// every load channel with a full client window (state.WindowSize posts) and
// keeps no more than that per channel: without it, the fake stored every
// churn post and the client's windows kept filling for hours (20 → 60 posts
// in ~100 channels), both reading as growth in the soak. Uploads are stored
// in filesDir, not in memory: the fakes share the client's process, and a
// memory check pasting large pictures measured the fake keeping each one.
// In a churn run collapsed reply threads alternate by fake (i is its index):
// on for the first, off for the next, and so on — replies go through the
// thread panel, the thread reads and the thread badges on one server and
// inline in the feed with their context line on the other (KeepPosts trims
// the replies and the threads of trimmed roots too).
func fakeOptions(o desktopOpts, i int, filesDir string) mmfake.Options {
	opts := mmfake.Options{ExtraChannels: o.FakeChannels, FilesDir: filesDir}
	if o.FakeChurn > 0 {
		opts.ExtraChannelPosts = state.WindowSize
		opts.KeepPosts = state.WindowSize
		opts.CRT = i%2 == 0
	}
	return opts
}
