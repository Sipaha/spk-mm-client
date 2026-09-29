package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// AutocompleteUsers is GET /users/autocomplete: with channelID (teamID is
// then required by the server) the channel's members, and in OutOfChannel
// the team's other members. Empty teamID/channelID/limit are left out.
func (c *Client) AutocompleteUsers(ctx context.Context, teamID, channelID, name string, limit int) (model.UserAutocomplete, error) {
	q := url.Values{"name": {name}}
	if teamID != "" {
		q.Set("in_team", teamID)
	}
	if channelID != "" {
		q.Set("in_channel", channelID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out model.UserAutocomplete
	return out, c.get(ctx, "/api/v4/users/autocomplete?"+q.Encode(), &out)
}

// AutocompleteChannels is GET /teams/{id}/channels/autocomplete: the team's
// public channels and the private ones we are in, matching name.
func (c *Client) AutocompleteChannels(ctx context.Context, teamID, name string) ([]model.Channel, error) {
	var out []model.Channel
	return out, c.get(ctx, "/api/v4/teams/"+url.PathEscape(teamID)+"/channels/autocomplete?"+url.Values{"name": {name}}.Encode(), &out)
}

// AutocompleteEmoji is GET /emoji/autocomplete: custom emoji whose name
// starts with name.
func (c *Client) AutocompleteEmoji(ctx context.Context, name string) ([]model.Emoji, error) {
	var out []model.Emoji
	return out, c.get(ctx, "/api/v4/emoji/autocomplete?"+url.Values{"name": {name}}.Encode(), &out)
}

// AutocompleteCommands is GET /teams/{id}/commands/autocomplete: the slash
// commands a team offers, built-in ones included.
func (c *Client) AutocompleteCommands(ctx context.Context, teamID string) ([]model.Command, error) {
	var out []model.Command
	return out, c.get(ctx, "/api/v4/teams/"+url.PathEscape(teamID)+"/commands/autocomplete", &out)
}

// ExecuteCommand runs a slash command (POST /commands/execute). Like every
// POST it is never retried on a transport error: a command may have acted.
func (c *Client) ExecuteCommand(ctx context.Context, args model.CommandArgs) (model.CommandResponse, error) {
	var out model.CommandResponse
	_, err := c.do(ctx, http.MethodPost, "/api/v4/commands/execute", args, &out)
	return out, err
}
