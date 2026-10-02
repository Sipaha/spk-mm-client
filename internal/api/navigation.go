package api

import (
	"context"
	"sort"

	"github.com/spk/spk-mm-client/internal/state"
)

type QuickChannelDTO struct {
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	state.QuickChannel
}

func (s *Service) QuickChannels(ctx context.Context) ([]QuickChannelDTO, error) {
	servers, err := s.st.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	out := []QuickChannelDTO{}
	m := s.manager()
	if m == nil {
		return out, nil
	}
	for _, srv := range servers {
		w := m.Worker(srv.ID)
		if w == nil {
			continue
		}
		for _, ch := range w.State().QuickChannels() {
			out = append(out, QuickChannelDTO{ServerID: srv.ID, ServerName: srv.Name, QuickChannel: ch})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ServerName < out[j].ServerName })
	return out, nil
}
