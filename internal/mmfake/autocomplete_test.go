package mmfake

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func usernames(us []model.User) []string {
	out := []string{}
	for _, u := range us {
		out = append(out, u.Username)
	}
	return out
}

func TestUsersAutocompleteSplitsByChannel(t *testing.T) {
	s := Start(Options{ExtraUsers: 1}) // dave: in town square, not in off-topic
	defer s.Close()
	a := loginAs(t, s, "alice")

	var res model.UserAutocomplete
	require.Equal(t, 200, a.call("GET", "/api/v4/users/autocomplete?in_team=t-fake&in_channel=c-offtopic&name=", nil, &res))
	assert.Equal(t, []string{"alice", "bob"}, usernames(res.Users))
	assert.Equal(t, []string{"carol", "dave"}, usernames(res.OutOfChannel))

	res = model.UserAutocomplete{}
	require.Equal(t, 200, a.call("GET", "/api/v4/users/autocomplete?in_team=t-fake&in_channel=c-offtopic&name=B", nil, &res))
	assert.Equal(t, []string{"bob"}, usernames(res.Users), "case-insensitive username prefix")
	assert.Empty(t, res.OutOfChannel)

	res = model.UserAutocomplete{}
	require.Equal(t, 200, a.call("GET", "/api/v4/users/autocomplete?in_team=t-fake&in_channel=c-offtopic&name=clark", nil, &res))
	assert.Equal(t, []string{"carol"}, usernames(res.OutOfChannel), "last name matches too")
	assert.Equal(t, "Carol", res.OutOfChannel[0].FirstName)

	require.Equal(t, 200, a.call("GET", "/api/v4/users/autocomplete?in_team=t-fake&in_channel=c-town&name=&limit=2", nil, &res))
	assert.Len(t, res.Users, 2, "limit")

	assert.Equal(t, 403, a.call("GET", "/api/v4/users/autocomplete?in_team=t-fake&in_channel=c-nope&name=", nil, nil))
}

func TestChannelsAutocompleteListsPublicAndOwnPrivate(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var chans []model.Channel
	require.Equal(t, 200, a.call("GET", "/api/v4/teams/t-fake/channels/autocomplete?name=off", nil, &chans))
	var names []string
	for _, c := range chans {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"off-topic", "offices"}, names, "joined off-topic and the public offices alice is not in")

	require.Equal(t, 200, a.call("GET", "/api/v4/teams/t-fake/channels/autocomplete?name=sec", nil, &chans))
	require.Len(t, chans, 1, "her own private channel")
	b := loginAs(t, s, "bob")
	require.Equal(t, 200, b.call("GET", "/api/v4/teams/t-fake/channels/autocomplete?name=sec", nil, &chans))
	assert.Empty(t, chans, "someone else's private channel is not listed")
}

func TestEmojiAutocomplete(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var em []model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji/autocomplete?name=party", nil, &em))
	require.Len(t, em, 1)
	assert.Equal(t, "partyparrot", em[0].Name)
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji/autocomplete?name=parrot", nil, &em))
	assert.Empty(t, em, "prefix only, like the server")

	off := Start(Options{DisableCustomEmoji: true})
	defer off.Close()
	assert.Equal(t, 501, loginAs(t, off, "alice").call("GET", "/api/v4/emoji/autocomplete?name=p", nil, nil))
}

func TestCommandsAutocompleteListsBuiltIns(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var cmds []model.Command
	require.Equal(t, 200, a.call("GET", "/api/v4/teams/t-fake/commands/autocomplete", nil, &cmds))
	var triggers []string
	for _, c := range cmds {
		triggers = append(triggers, c.Trigger)
		assert.NotEmpty(t, c.AutoCompleteDesc)
	}
	assert.Equal(t, []string{"away", "echo", "shrug"}, triggers)
	assert.Equal(t, 403, a.call("GET", "/api/v4/teams/t-other/commands/autocomplete", nil, nil))
}

func TestExecuteEchoPostsAsTheUser(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var resp model.CommandResponse
	require.Equal(t, 200, a.call("POST", "/api/v4/commands/execute",
		model.CommandArgs{ChannelID: "c-town", TeamID: "t-fake", Command: "/echo hi there"}, &resp))
	posts := s.VisiblePosts("c-town")
	require.NotEmpty(t, posts)
	last := posts[len(posts)-1]
	assert.Equal(t, "hi there", last.Message)
	assert.Equal(t, "u-alice", last.UserID)
	assert.Equal(t, []ExecutedCommand{{UserID: "u-alice", ChannelID: "c-town", TeamID: "t-fake", Command: "/echo hi there"}}, s.ExecutedCommands())
}

func TestExecuteInAThreadRepliesToTheRoot(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.PostAs("c-town", "bob", "root")
	a := loginAs(t, s, "alice")
	require.Equal(t, 200, a.call("POST", "/api/v4/commands/execute",
		model.CommandArgs{ChannelID: "c-town", RootID: root.ID, Command: "/shrug ok"}, nil))
	posts := s.VisiblePosts("c-town")
	last := posts[len(posts)-1]
	assert.Equal(t, root.ID, last.RootID)
	assert.Equal(t, `ok ¯\\\_(ツ)\_/¯`, last.Message)
}

func TestExecuteAwaySendsAnEphemeralPostToTheUserOnly(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	require.Equal(t, "hello", read(t, c).Event)
	var resp model.CommandResponse
	require.Equal(t, 200, a.call("POST", "/api/v4/commands/execute",
		model.CommandArgs{ChannelID: "c-town", TeamID: "t-fake", RootID: "", Command: "/away"}, &resp))
	assert.Equal(t, "ephemeral", resp.ResponseType)
	f := read(t, c)
	for f.Event == "status_change" { // /away sets the status first
		f = read(t, c)
	}
	require.Equal(t, "ephemeral_message", f.Event)
	var p model.Post
	require.NoError(t, json.Unmarshal([]byte(f.Data["post"].(string)), &p))
	assert.Equal(t, model.PostTypeEphemeral, p.Type)
	assert.Equal(t, "c-town", p.ChannelID)
	assert.Equal(t, "You are now away", p.Message)
	assert.NotEmpty(t, p.ID)
	assert.Empty(t, s.VisiblePosts("c-town"), "never stored")
	for _, ev := range s.Events() {
		if ev.Name == "ephemeral_message" {
			assert.Equal(t, []string{"u-alice"}, ev.To)
		}
	}
}

func TestExecuteUnknownCommandIsNotFound(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	status, id := a.callErr("POST", "/api/v4/commands/execute", model.CommandArgs{ChannelID: "c-town", Command: "/nope x"})
	assert.Equal(t, 404, status)
	assert.Equal(t, "api.command.execute_command.not_found.app_error", id)
	status, _ = a.callErr("POST", "/api/v4/commands/execute", model.CommandArgs{ChannelID: "c-town", Command: "echo"})
	assert.Equal(t, 400, status, "must start with a slash")
	status, _ = a.callErr("POST", "/api/v4/commands/execute", model.CommandArgs{ChannelID: "c-offtopic", Command: "/echo x"})
	assert.Equal(t, 200, status)
	b := loginAs(t, s, "carol")
	status, _ = b.callErr("POST", "/api/v4/commands/execute", model.CommandArgs{ChannelID: "c-offtopic", Command: "/echo x"})
	assert.Equal(t, 403, status, "not a member")
}
