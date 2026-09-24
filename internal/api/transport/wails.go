//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-mattermost/internal/api"
)

// API is the Wails service. Bindings are addressed by Go FQN:
//
//	github.com/spk/spk-mattermost/internal/api/transport.API.<Method>
//
// (mirrored in frontend/src/api/client.ts).
type API struct{ a api.API }

func NewAPI(a api.API) *API { return &API{a: a} }

func (w *API) ListServers() ([]api.ServerDTO, error) { return w.a.ListServers(context.Background()) }
func (w *API) AddServer(url string) (api.ServerDTO, error) {
	return w.a.AddServer(context.Background(), url)
}
func (w *API) RemoveServer(id int64) error     { return w.a.RemoveServer(context.Background(), id) }
func (w *API) StartGitLabLogin(id int64) error { return w.a.StartGitLabLogin(context.Background(), id) }
func (w *API) LoginWithPassword(id int64, login, password string) (api.ServerDTO, error) {
	return w.a.LoginWithPassword(context.Background(), id, login, password)
}
func (w *API) Logout(id int64) error { return w.a.Logout(context.Background(), id) }
