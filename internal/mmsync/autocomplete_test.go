package mmsync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

func liveHarness(t *testing.T, o mmfake.Options) *harness {
	h := newHarness(t, o)
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h
}

func acNames(us []ACUser) []string {
	out := []string{}
	for _, u := range us {
		out = append(out, u.Username)
	}
	return out
}

func TestAutocompleteUsersSplitByChannelWithStatus(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	res, err := h.w.Autocomplete(context.Background(), ACUsers, "c-offtopic", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "bob"}, acNames(res.Users))
	assert.Equal(t, []string{"carol"}, acNames(res.Others))
	assert.True(t, res.Users[0].Me)
	bob, carol := res.Users[1], res.Others[0]
	assert.Equal(t, "Bob Brown", bob.FullName)
	assert.NotEmpty(t, bob.Avatar, "picture version for /media avatars")
	assert.Equal(t, "online", bob.Status)
	assert.Equal(t, "away", carol.Status, "statuses the worker did not know are read")
}

func TestAutocompleteUsersPutsUsernamePrefixFirst(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	res, err := h.w.Autocomplete(context.Background(), ACUsers, "c-town", "c")
	require.NoError(t, err)
	// "c": carol (username, Carol Clark); bob (Bob Brown) and alice not.
	assert.Equal(t, []string{"carol"}, acNames(res.Users))
	res, err = h.w.Autocomplete(context.Background(), ACUsers, "c-town", "b")
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, acNames(res.Users))
}

func TestAutocompleteIsCachedPerPrefixForAMinute(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.clock = &clock{}
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	ctx := context.Background()
	hits := func() int { return h.fake.Hits("GET", "/api/v4/users/autocomplete") }
	_, err := h.w.Autocomplete(ctx, ACUsers, "c-town", "b")
	require.NoError(t, err)
	_, err = h.w.Autocomplete(ctx, ACUsers, "c-town", "b")
	require.NoError(t, err)
	assert.Equal(t, 1, hits(), "the same prefix is answered from the cache")
	_, err = h.w.Autocomplete(ctx, ACUsers, "c-town", "bo")
	require.NoError(t, err)
	_, err = h.w.Autocomplete(ctx, ACUsers, "c-offtopic", "b")
	require.NoError(t, err)
	assert.Equal(t, 3, hits(), "another prefix or channel asks again")
	h.clock.advance(acCacheTTL + time.Second)
	_, err = h.w.Autocomplete(ctx, ACUsers, "c-town", "b")
	require.NoError(t, err)
	assert.Equal(t, 4, hits(), "expired after a minute")
}

func TestAutocompleteRequestIsCancelledWithItsContext(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.fake.SetLatency("/users/autocomplete", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := h.w.Autocomplete(ctx, ACUsers, "c-town", "b")
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "a stale request ends with its caller")
	assert.Equal(t, 0, h.w.ac.len(), "nothing cached from a cancelled request")
}

func TestAutocompleteChannelsJoinedFirst(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	res, err := h.w.Autocomplete(context.Background(), ACChannels, "c-town", "off")
	require.NoError(t, err)
	require.Len(t, res.Channels, 2)
	assert.Equal(t, ACChannel{ID: "c-offtopic", Name: "off-topic", DisplayName: "Off-Topic", Type: "O", Joined: true}, res.Channels[0])
	assert.Equal(t, ACChannel{ID: "c-offices", Name: "offices", DisplayName: "Offices", Type: "O"}, res.Channels[1])
	res, err = h.w.Autocomplete(context.Background(), ACChannels, "c-dm-bob", "sec")
	require.NoError(t, err)
	require.Len(t, res.Channels, 1, "a DM searches the current team")
	assert.True(t, res.Channels[0].Joined)
}

func TestAutocompleteCommandsFilterOneCachedList(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	ctx := context.Background()
	res, err := h.w.Autocomplete(ctx, ACCommands, "c-town", "")
	require.NoError(t, err)
	var triggers []string
	for _, c := range res.Commands {
		triggers = append(triggers, c.Trigger)
	}
	assert.Equal(t, []string{"away", "echo", "leave", "logout", "shrug"}, triggers)
	res, err = h.w.Autocomplete(ctx, ACCommands, "c-town", "EC")
	require.NoError(t, err)
	require.Len(t, res.Commands, 1)
	assert.Equal(t, ACCommand{Trigger: "echo", Hint: `"message" [delay in seconds]`, Description: "Echo back text from your account"}, res.Commands[0])
	res, err = h.w.Autocomplete(ctx, ACCommands, "c-dm-bob", "s")
	require.NoError(t, err)
	require.Len(t, res.Commands, 1)
	assert.Equal(t, 1, h.fake.Hits("GET", "/api/v4/teams/t-fake/commands/autocomplete"), "one list per team, filtered locally")
}

func TestAutocompleteEmojiMergesTheIndexAndTheServer(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.eventually(func() bool { return len(h.w.State().CustomEmojiNames()) > 0 }, "custom emoji index")
	res, err := h.w.Autocomplete(context.Background(), ACEmoji, "c-town", "parrot")
	require.NoError(t, err)
	assert.Equal(t, []string{"partyparrot"}, res.Emoji, "the index matches anywhere in the name")
	res, err = h.w.Autocomplete(context.Background(), ACEmoji, "c-town", "party")
	require.NoError(t, err)
	assert.Equal(t, []string{"partyparrot"}, res.Emoji, "one name once")
	assert.Equal(t, 2, h.fake.Hits("GET", "/api/v4/emoji/autocomplete"))
}

func TestAutocompleteOfflineIsLocalOnly(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.eventually(func() bool { return len(h.w.State().CustomEmojiNames()) > 0 }, "custom emoji index")
	h.fake.SetDown(true)
	h.w.Nudge()
	h.eventually(func() bool { return h.w.Status() != StatusLive }, "never went offline")
	before := h.fake.Hits("GET", "/api/v4/users/autocomplete")
	res, err := h.w.Autocomplete(context.Background(), ACUsers, "c-town", "b")
	require.NoError(t, err, "offline is not an error: the popup is just empty")
	assert.Empty(t, res.Users)
	assert.Equal(t, before, h.fake.Hits("GET", "/api/v4/users/autocomplete"), "no request while offline")
	res, err = h.w.Autocomplete(context.Background(), ACEmoji, "c-town", "parr")
	require.NoError(t, err)
	assert.Equal(t, []string{"partyparrot"}, res.Emoji, "custom emoji from the index")
}

func TestAutocompleteCacheIsDroppedWhenTheWorkerStops(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	_, err := h.w.Autocomplete(context.Background(), ACUsers, "c-town", "b")
	require.NoError(t, err)
	require.Positive(t, h.w.ac.len())
	h.stop()
	assert.Equal(t, 0, h.w.ac.len(), "sign-out stops the worker: nothing stays")
}

func TestAutocompleteUnknownChannel(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	_, err := h.w.Autocomplete(context.Background(), ACUsers, "c-nope", "b")
	assert.ErrorIs(t, err, ErrNoChannel)
}

func TestExecuteCommandCarriesTeamAndThreadRoot(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	root := h.fake.PostAs("c-town", "bob", "a root")
	ctx := context.Background()
	require.NoError(t, h.w.ExecuteCommand(ctx, "c-town", root.ID, "/shrug  ok "))
	require.NoError(t, h.w.ExecuteCommand(ctx, "c-dm-bob", "", "/ECHO hi"))
	got := h.fake.ExecutedCommands()
	require.Len(t, got, 2)
	assert.Equal(t, mmfake.ExecutedCommand{UserID: "u-alice", ChannelID: "c-town", TeamID: "t-fake", RootID: root.ID, Command: "/shrug ok"}, got[0],
		"the webapp's normalisation: the trigger lowercased, the rest trimmed")
	assert.Equal(t, mmfake.ExecutedCommand{UserID: "u-alice", ChannelID: "c-dm-bob", TeamID: "t-fake", Command: "/echo hi"}, got[1],
		"a DM runs in the current team")
}

func TestExecuteUnknownCommandIsReported(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	err := h.w.ExecuteCommand(context.Background(), "c-town", "", "/nope")
	assert.ErrorIs(t, err, ErrCommandNotFound)
	assert.Empty(t, h.fake.ExecutedCommands())
}

func hasEphemeral(posts []state.PostView, msg string) bool {
	for _, p := range posts {
		if p.Ephemeral && p.Message == msg {
			return true
		}
	}
	return false
}

func TestEphemeralAnswerShowsInTheChannelAndTheThread(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	ctx := context.Background()
	require.NoError(t, h.w.ExecuteCommand(ctx, "c-town", "", "/away"))
	h.eventually(func() bool { return hasEphemeral(h.view("c-town").Posts, "You are now away") }, "the ephemeral answer never showed")

	root := h.fake.PostAs("c-town", "bob", "a root")
	h.eventually(func() bool { return h.hasMessage("c-town", "a root") }, "root")
	_, ok := h.w.OpenThread("c-town", root.ID)
	require.True(t, ok)
	h.eventually(func() bool { v, _ := h.w.State().ThreadView(root.ID); return v.Loaded }, "thread")
	require.NoError(t, h.w.ExecuteCommand(ctx, "c-town", root.ID, "/away"))
	h.eventually(func() bool {
		v, _ := h.w.State().ThreadView(root.ID)
		return hasEphemeral(v.Posts, "You are now away")
	}, "the thread's ephemeral answer never showed in the panel")
}

// Review I1: the server's /leave ignores root_id and leaves the whole
// channel; the webapp refuses it in a thread, so do we — nothing is sent.
func TestLeaveInAThreadIsRefusedWithoutASend(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	root := h.fake.PostAs("c-town", "bob", "a root")
	for _, cmd := range []string{"/leave", "/LEAVE  now"} {
		assert.ErrorIs(t, h.w.ExecuteCommand(context.Background(), "c-town", root.ID, cmd), ErrUnsupportedInThread)
	}
	assert.Empty(t, h.fake.ExecutedCommands())
	assert.ErrorIs(t, h.w.ExecuteCommand(context.Background(), "c-town", root.ID, "/leaves"), ErrCommandNotFound, "only /leave itself is refused; this one reaches the server")
}

// Review M1: a request in flight when the worker stops (sign-out, removal,
// a new sign-in) ends with it — it must not keep using the old session.
func TestAutocompleteAndCommandsEndWithTheWorker(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.fake.SetLatency("/users/autocomplete", 5*time.Second)
	h.fake.SetLatency("/commands/execute", 5*time.Second)
	errs := make(chan error, 2)
	go func() {
		_, err := h.w.Autocomplete(context.Background(), ACUsers, "c-town", "b")
		errs <- err
	}()
	go func() { errs <- h.w.ExecuteCommand(context.Background(), "c-town", "", "/echo x") }()
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	h.stop()
	for range 2 {
		select {
		case err := <-errs:
			assert.Error(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("a request outlived its worker")
		}
	}
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, 0, h.w.ac.len())
}

// Review M3: a DM's @ searches the team on screen; its cached answer is
// per team, not reused after a team switch.
func TestDMUsersCacheIsPerTeam(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	ctx := context.Background()
	_, err := h.w.Autocomplete(ctx, ACUsers, "c-dm-bob", "b")
	require.NoError(t, err)
	team, _ := h.w.State().AutocompleteScope("c-dm-bob")
	assert.Contains(t, acUsersKey("c-dm-bob", team, "b"), team)
	assert.NotEqual(t, acUsersKey("c-dm-bob", "t1", "b"), acUsersKey("c-dm-bob", "t2", "b"))
}
