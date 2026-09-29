package api

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutocompleteThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")

	res, err := f.svc.Autocomplete(ctx, id, "users", "c-offtopic", "", "b")
	require.NoError(t, err)
	require.Len(t, res.Users, 1)
	assert.Equal(t, "bob", res.Users[0].Username)

	res, err = f.svc.Autocomplete(ctx, id, "channels", "c-town", "", "off")
	require.NoError(t, err)
	assert.Len(t, res.Channels, 2)

	res, err = f.svc.Autocomplete(ctx, id, "commands", "c-town", "", "ec")
	require.NoError(t, err)
	require.Len(t, res.Commands, 1)
	assert.Equal(t, "echo", res.Commands[0].Trigger)
}

func TestAutocompleteValidatesItsInput(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")
	before := fake.Hits("GET", "/api/v4/users/autocomplete")

	_, err := f.svc.Autocomplete(ctx, id, "files", "c-town", "", "b")
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "unknown kind")
	_, err = f.svc.Autocomplete(ctx, id, "users", "../x", "", "b")
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "not an id")
	_, err = f.svc.Autocomplete(ctx, id, "users", "c-town", "r/1", "b")
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "root not an id")
	_, err = f.svc.Autocomplete(ctx, id, "users", "c-nope", "", "b")
	assert.Equal(t, CodeNoChannel, codeOf(err))
	_, err = f.svc.Autocomplete(ctx, 999, "users", "c-town", "", "b")
	assert.Error(t, err)

	for _, p := range []string{strings.Repeat("b", MaxAutocompletePrefix+1), "b\nc", "b c", "b\x00", "\xff"} {
		res, err := f.svc.Autocomplete(ctx, id, "users", "c-town", "", p)
		require.NoError(t, err, "%q", p)
		assert.Empty(t, res.Users, "%q: nothing to suggest, nothing asked", p)
	}
	assert.Equal(t, before, fake.Hits("GET", "/api/v4/users/autocomplete"))
	_, err = f.svc.Autocomplete(ctx, id, "users", "c-town", "", strings.Repeat("b", MaxAutocompletePrefix))
	require.NoError(t, err)
	assert.Equal(t, before+1, fake.Hits("GET", "/api/v4/users/autocomplete"), "the longest allowed prefix is asked")
}

func TestExecuteCommandThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")

	require.NoError(t, f.svc.ExecuteCommand(ctx, id, "c-town", "", "/echo hello from a command"))
	f.eventually(func() bool { return f.has(id, "c-town", "hello from a command") }, "echo never posted")

	assert.Equal(t, CodeCommandNotFound, codeOf(f.svc.ExecuteCommand(ctx, id, "c-town", "", "/nope")))
	assert.Equal(t, CodeInvalidArgument, codeOf(f.svc.ExecuteCommand(ctx, id, "c-town", "", "echo x")), "must start with a slash")
	assert.Equal(t, CodeInvalidArgument, codeOf(f.svc.ExecuteCommand(ctx, id, "c-town", "", "/")))
	assert.Equal(t, CodeInvalidArgument, codeOf(f.svc.ExecuteCommand(ctx, id, "c-town", "", "/echo "+strings.Repeat("x", MaxCommandRunes))))
	assert.Equal(t, CodeNoChannel, codeOf(f.svc.ExecuteCommand(ctx, id, "c-nope", "", "/echo x")))
	assert.Equal(t, CodeNoPost, codeOf(f.svc.ExecuteCommand(ctx, id, "c-town", "not-held", "/echo x")), "a thread the panel does not hold")
	assert.Len(t, fake.ExecutedCommands(), 1, "refused ones never reach the server")
}

func TestExecuteCommandInAHeldThread(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	root := fake.SeedThread("c-town", "alice", 1)
	_, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	require.NoError(t, f.svc.ExecuteCommand(ctx, id, "c-town", root, "/away"))
	got := fake.ExecutedCommands()
	require.Len(t, got, 1)
	assert.Equal(t, root, got[0].RootID)
	f.eventually(func() bool {
		v, err := f.svc.GetThread(ctx, id, root)
		if err != nil {
			return false
		}
		for _, p := range v.Posts {
			if p.Ephemeral && p.Message == "You are now away" {
				return true
			}
		}
		return false
	}, "the ephemeral answer never reached the thread")
}

func TestSignOutEndsTheAutocompleteSession(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")
	_, err := f.svc.Autocomplete(ctx, id, "users", "c-town", "", "b")
	require.NoError(t, err)
	require.NoError(t, f.svc.Logout(ctx, id))
	_, err = f.svc.Autocomplete(ctx, id, "users", "c-town", "", "b")
	assert.Equal(t, CodeNotSignedIn, codeOf(err), "no worker, no cache, no answer")
	_, err = f.svc.LoginWithPassword(ctx, id, "alice", "secret")
	require.NoError(t, err)
	f.eventually(func() bool { return f.server(id).State == "live" }, "live again")
	hits := fake.Hits("GET", "/api/v4/users/autocomplete")
	_, err = f.svc.Autocomplete(ctx, id, "users", "c-town", "", "b")
	require.NoError(t, err)
	assert.Equal(t, hits+1, fake.Hits("GET", "/api/v4/users/autocomplete"), "a new session asks again")
}
