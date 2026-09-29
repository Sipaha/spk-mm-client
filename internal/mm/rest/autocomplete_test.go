package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestAutocompleteUsersQuery(t *testing.T) {
	var got []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/autocomplete", r.URL.Path)
		got = append(got, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(model.UserAutocomplete{Users: []model.User{{ID: "u1", Username: "bob"}},
			OutOfChannel: []model.User{{ID: "u2", Username: "bobby"}}})
	})
	res, err := c.AutocompleteUsers(context.Background(), "t 1", "c1", "bo b", 25)
	require.NoError(t, err)
	assert.Equal(t, "bob", res.Users[0].Username)
	assert.Equal(t, "bobby", res.OutOfChannel[0].Username)
	_, err = c.AutocompleteUsers(context.Background(), "", "", "x", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"in_channel=c1&in_team=t+1&limit=25&name=bo+b",
		"name=x",
	}, got, "escaped; team/channel/limit only when given")
}

func TestAutocompleteChannelsEmojiAndCommandsPaths(t *testing.T) {
	var got []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/api/v4/teams/t/1/channels/autocomplete", "/api/v4/teams/t%2F1/channels/autocomplete":
			_, _ = w.Write([]byte(`[{"id":"c1","name":"off-topic","display_name":"Off-Topic","type":"O"}]`))
		case "/api/v4/emoji/autocomplete":
			_, _ = w.Write([]byte(`[{"id":"e1","name":"partyparrot"}]`))
		default:
			_, _ = w.Write([]byte(`[{"trigger":"echo","auto_complete":true,"auto_complete_hint":"text","auto_complete_desc":"Echo"}]`))
		}
	})
	ctx := context.Background()
	chans, err := c.AutocompleteChannels(ctx, "t/1", "of f")
	require.NoError(t, err)
	assert.Equal(t, "off-topic", chans[0].Name)
	em, err := c.AutocompleteEmoji(ctx, "par")
	require.NoError(t, err)
	assert.Equal(t, "partyparrot", em[0].Name)
	cmds, err := c.AutocompleteCommands(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, model.Command{Trigger: "echo", AutoComplete: true, AutoCompleteHint: "text", AutoCompleteDesc: "Echo"}, cmds[0])
	assert.Equal(t, []string{
		"GET /api/v4/teams/t%2F1/channels/autocomplete?name=of+f",
		"GET /api/v4/emoji/autocomplete?name=par",
		"GET /api/v4/teams/t1/commands/autocomplete?",
	}, got)
}

func TestExecuteCommandPostsArgsAndIsNotRetried(t *testing.T) {
	calls := 0
	var body model.CommandArgs
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v4/commands/execute", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if calls == 1 {
			_, _ = w.Write([]byte(`{"response_type":"ephemeral","text":"You are now away"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"id":"api.command.execute_command.not_found.app_error","message":"not found"}`))
	})
	resp, err := c.ExecuteCommand(context.Background(), model.CommandArgs{ChannelID: "c1", TeamID: "t1", RootID: "r1", Command: "/away"})
	require.NoError(t, err)
	assert.Equal(t, model.CommandArgs{ChannelID: "c1", TeamID: "t1", RootID: "r1", Command: "/away"}, body)
	assert.Equal(t, "ephemeral", resp.ResponseType)

	_, err = c.ExecuteCommand(context.Background(), model.CommandArgs{ChannelID: "c1", Command: "/nope"})
	var re *Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, "api.command.execute_command.not_found.app_error", re.ID)
	assert.Equal(t, 2, calls)
}
