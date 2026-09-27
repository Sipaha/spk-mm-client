package api

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/store"
)

type chatFixture struct {
	t     *testing.T
	svc   *Service
	st    *store.Store
	evs   <-chan events.Event
	notes *RecordingNotifier
	badge chan Badge
}

func newChatFixture(t *testing.T) *chatFixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	ch, unsub := em.Subscribe()
	t.Cleanup(unsub)
	f := &chatFixture{t: t, st: st, evs: ch, notes: &RecordingNotifier{}, badge: make(chan Badge, 64)}
	f.svc = NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	f.svc.tune = func(c *mmsync.Config) {
		c.FlushEvery, c.MinBackoff, c.MaxBackoff, c.WakeCheck = 50*time.Millisecond, 20*time.Millisecond, 200*time.Millisecond, time.Hour
	}
	f.svc.nq.window = 150 * time.Millisecond
	f.svc.SetNotifier(f.notes)
	f.svc.OnBadge(func(b Badge) { f.badge <- b })
	require.NoError(t, f.svc.Start(context.Background()))
	t.Cleanup(f.svc.Close)
	return f
}

// signIn adds a fake server and signs alice in; returns the server id.
func (f *chatFixture) signIn(fake *mmfake.Server, user string) int64 {
	f.t.Helper()
	ctx := context.Background()
	srv, err := f.svc.AddServer(ctx, fake.URL())
	require.NoError(f.t, err)
	_, err = f.svc.LoginWithPassword(ctx, srv.ID, user, "secret")
	require.NoError(f.t, err)
	return srv.ID
}

func (f *chatFixture) server(id int64) ServerDTO {
	list, err := f.svc.ListServers(context.Background())
	require.NoError(f.t, err)
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	f.t.Fatalf("server %d not listed", id)
	return ServerDTO{}
}

func (f *chatFixture) eventually(cond func() bool, msg string) {
	f.t.Helper()
	require.Eventually(f.t, cond, 10*time.Second, 20*time.Millisecond, msg)
}

func (f *chatFixture) loaded(id int64, channelID string) bool {
	v, err := f.svc.GetChannel(context.Background(), id, channelID)
	return err == nil && v.Loaded && !v.Syncing
}

func (f *chatFixture) has(id int64, channelID, msg string) bool {
	v, err := f.svc.GetChannel(context.Background(), id, channelID)
	if err != nil {
		return false
	}
	for _, p := range v.Posts {
		if p.Message == msg && !p.Pending {
			return true
		}
	}
	return false
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func startFake(t *testing.T) *mmfake.Server {
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	return fake
}

func TestChatThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "server never went live")
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "town square not prefetched")

	sb, err := f.svc.Sidebar(ctx, id, "")
	require.NoError(t, err)
	assert.Equal(t, "t-fake", sb.TeamID)
	assert.Equal(t, "c-town", sb.SelectedChannelID)

	v, err := f.svc.OpenChannel(ctx, id, "c-town")
	require.NoError(t, err)
	assert.Len(t, v.Posts, 60)
	assert.Equal(t, "t-fake", v.TeamID)
	assert.Equal(t, "fake", v.TeamName)

	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "hello from service", nil))
	f.eventually(func() bool { return f.has(id, "c-offtopic", "hello from service") }, "sent post not visible")

	assert.Equal(t, CodeEmptyMessage, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "  \n ", nil)))
	_, err = f.svc.OpenChannel(ctx, id, "c-nope")
	assert.Equal(t, CodeNoChannel, codeOf(err))
}

func TestSignedOutServerHasNoChat(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	srv, err := f.svc.AddServer(context.Background(), fake.URL())
	require.NoError(t, err)
	assert.Equal(t, "off", f.server(srv.ID).State)
	_, err = f.svc.Sidebar(context.Background(), srv.ID, "")
	assert.Equal(t, CodeNotSignedIn, codeOf(err))
	_, err = f.svc.Sidebar(context.Background(), 999, "")
	assert.Equal(t, CodeNotFound, codeOf(err))
}

func TestMentionUpdatesBadgeAndNotifies(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	fake.PostAs("c-offtopic", "bob", "hi @alice")
	f.eventually(func() bool { return f.server(id).Mentions == 1 }, "server badge")
	var last Badge
	f.eventually(func() bool {
		for {
			select {
			case last = <-f.badge:
			default:
				return last.Mentions == 1 && last.Unread
			}
		}
	}, "OnBadge total")
	f.eventually(func() bool { return len(f.notes.List()) == 1 }, "notification")
	n := f.notes.List()[0]
	assert.Equal(t, Notification{ID: "mm-" + itoa(id) + "-c-offtopic", Title: "Off-Topic", Body: "bob: hi @alice", ServerID: id, ChannelID: "c-offtopic"}, n)
}

func TestFocusedActiveChannelIsReadAndSilent(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	require.NoError(t, f.svc.SelectServer(ctx, id))
	_, err := f.svc.OpenChannel(ctx, id, "c-offtopic")
	require.NoError(t, err)
	require.NoError(t, f.svc.SetFocused(ctx, true))
	fake.PostAs("c-offtopic", "bob", "hi @alice while you look")
	f.eventually(func() bool { return f.has(id, "c-offtopic", "hi @alice while you look") }, "post")
	f.eventually(func() bool {
		return fake.Member("c-offtopic", "alice").MsgCount == fake.Channel("c-offtopic").TotalMsgCount
	}, "focused active channel is marked read")
	time.Sleep(300 * time.Millisecond)
	assert.Empty(t, f.notes.List(), "no notification for the channel on screen")
}

func TestFocusGoesOnlyToTheActiveServer(t *testing.T) {
	f := newChatFixture(t)
	fakeA, fakeB := startFake(t), startFake(t)
	a, b := f.signIn(fakeA, "alice"), f.signIn(fakeB, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(a, "c-town") && f.loaded(b, "c-offtopic") }, "prefetch")

	require.NoError(t, f.svc.SelectServer(ctx, b))
	_, err := f.svc.OpenChannel(ctx, b, "c-offtopic")
	require.NoError(t, err)
	require.NoError(t, f.svc.SelectServer(ctx, a))
	_, err = f.svc.OpenChannel(ctx, a, "c-town")
	require.NoError(t, err)
	require.NoError(t, f.svc.SetFocused(ctx, true))

	fakeB.PostAs("c-offtopic", "bob", "background server post")
	f.eventually(func() bool { return f.has(b, "c-offtopic", "background server post") }, "post on B")
	time.Sleep(300 * time.Millisecond)
	assert.True(t, f.server(b).Unread, "B's channel is not on screen: stays unread")
	assert.Less(t, fakeB.Member("c-offtopic", "alice").MsgCount, fakeB.Channel("c-offtopic").TotalMsgCount)

	require.NoError(t, f.svc.SelectServer(ctx, b))
	f.eventually(func() bool { return !f.server(b).Unread }, "switching to B reads its open channel")
}

func TestCoalescedChannelEvents(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	time.Sleep(300 * time.Millisecond)
	for len(f.evs) > 0 {
		<-f.evs
	}
	for i := 0; i < 5; i++ {
		fake.PostAs("c-offtopic", "bob", "burst")
	}
	deadline := time.After(time.Second)
	channelEvents, sidebarEvents := 0, 0
loop:
	for {
		select {
		case ev := <-f.evs:
			switch {
			case ev.Type == EventChannelChanged && ev.Payload["channel_id"] == "c-offtopic":
				channelEvents++
			case ev.Type == EventSidebarChanged:
				sidebarEvents++
			}
		case <-deadline:
			break loop
		}
	}
	assert.GreaterOrEqual(t, channelEvents, 1)
	assert.LessOrEqual(t, channelEvents, 2, "5 posts in a burst → 1–2 events, not 5")
	assert.GreaterOrEqual(t, sidebarEvents, 1)
}

func TestLogoutStopsSyncAndClearsCache(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	f.eventually(func() bool { e, _ := f.st.LoadCache(ctx, id); return len(e) > 0 }, "snapshot written")
	require.NoError(t, f.svc.Logout(ctx, id))
	assert.Equal(t, "off", f.server(id).State)
	entries, err := f.st.LoadCache(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, entries)
	_, err = f.svc.Sidebar(ctx, id, "")
	assert.Equal(t, CodeNotSignedIn, codeOf(err))
}

func TestSigningInAsAnotherUserDropsCache(t *testing.T) {
	f := newChatFixture(t)
	f.svc.tune = func(c *mmsync.Config) { c.FlushEvery, c.WakeCheck = time.Hour, time.Hour } // flush only on stop
	require.NoError(t, f.svc.Start(context.Background()))                                    // re-create the manager with the new tuning
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-secret") }, "alice's private channel loaded")
	_, err := f.svc.LoginWithPassword(ctx, id, "bob", "secret")
	require.NoError(t, err)
	entries, err := f.st.LoadCache(ctx, id)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, "c-secret", e.Key, "alice's cache must not survive bob's sign-in")
	}
}

func TestRemoveServerStopsItsWorker(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")
	require.NoError(t, f.svc.RemoveServer(context.Background(), id))
	assert.Nil(t, f.svc.manager().Worker(id))
}

func TestOpenURLAllowsOnlyWebAndMail(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.svc.OpenURL(ctx, "https://example.com/a?b=1"))
	require.NoError(t, f.svc.OpenURL(ctx, "mailto:bob@example.com"))
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://", "mmauth://callback", "::"} {
		assert.Equal(t, CodeInvalidURL, codeOf(f.svc.OpenURL(ctx, bad)), bad)
	}
	assert.Equal(t, []string{"https://example.com/a?b=1", "mailto:bob@example.com"}, f.opened)
}

func TestNotificationClickOpensChannel(t *testing.T) {
	f := newFixture(t)
	f.svc.NotificationClicked(3, "c-town")
	ev := f.nextEvent(t, EventOpenChannel)
	assert.Equal(t, map[string]any{"server_id": int64(3), "channel_id": "c-town"}, ev.Payload)
}

// Switching servers by opening a channel must view that channel only — not
// the one the other server had open before (never shown in this session).
func TestOpenChannelOnAnotherServerViewsOnlyThatChannel(t *testing.T) {
	f := newChatFixture(t)
	fakeA, fakeB := startFake(t), startFake(t)
	a, b := f.signIn(fakeA, "alice"), f.signIn(fakeB, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(a, "c-town") && f.loaded(b, "c-offtopic") && f.loaded(b, "c-town") }, "prefetch")
	var mu sync.Mutex
	var viewedOnB []string // B's active channel each time B's worker got focus (SetFocused(true) views it)
	f.svc.onFocus = func(id int64, active string) {
		if id == b {
			mu.Lock()
			viewedOnB = append(viewedOnB, active)
			mu.Unlock()
		}
	}

	_, err := f.svc.OpenChannel(ctx, b, "c-offtopic") // unfocused: B's active channel is now Y, nothing viewed
	require.NoError(t, err)
	_, err = f.svc.OpenChannel(ctx, a, "c-town")
	require.NoError(t, err)
	require.NoError(t, f.svc.SetFocused(ctx, true))

	fakeB.PostAs("c-offtopic", "bob", "unread on Y")
	fakeB.PostAs("c-town", "bob", "unread on X")
	f.eventually(func() bool { return f.has(b, "c-offtopic", "unread on Y") && f.has(b, "c-town", "unread on X") }, "posts on B")

	_, err = f.svc.OpenChannel(ctx, b, "c-town")
	require.NoError(t, err)
	f.eventually(func() bool {
		return fakeB.Member("c-town", "alice").MsgCount == fakeB.Channel("c-town").TotalMsgCount
	}, "the opened channel X is viewed")
	mu.Lock()
	assert.Equal(t, []string{"c-town"}, viewedOnB, "B was focused only with X active, never with Y")
	mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	assert.Less(t, fakeB.Member("c-offtopic", "alice").MsgCount, fakeB.Channel("c-offtopic").TotalMsgCount,
		"B's previously active channel Y was never shown: stays unread")
}

// A re-login that fails to save the new session must leave the server
// syncing with the stored (now revoked) token — needs_reauth, not "off"
// while the store still says signed in.
func TestFailedSessionSaveRestartsSync(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")
	require.NoError(t, f.st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_session BEFORE UPDATE OF token ON servers
			BEGIN SELECT RAISE(ABORT, 'injected'); END`)
		return err
	}))
	_, err := f.svc.LoginWithPassword(ctx, id, "alice", "secret")
	assert.Equal(t, CodeInternal, codeOf(err))
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "sync restarted with the stored token")
}

// A late subscriber (the tray registers after D-Bus probe and GTK init) must
// get the current total at once: after startup the callbacks fire only when
// the total changes, so a restored unread badge would otherwise never show.
func TestOnBadgeReplaysCurrentTotal(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	fake.PostAs("c-offtopic", "bob", "hi @alice")
	f.eventually(func() bool {
		f.svc.mu.Lock()
		defer f.svc.mu.Unlock()
		return f.svc.total.Mentions == 1
	}, "total badge")

	var mu sync.Mutex
	var got []Badge
	f.svc.OnBadge(func(b Badge) { mu.Lock(); got = append(got, b); mu.Unlock() })
	mu.Lock() // the replay is synchronous: already delivered when OnBadge returns
	defer mu.Unlock()
	require.NotEmpty(t, got)
	assert.Equal(t, Badge{Unread: true, Mentions: 1}, got[0])
}

// actionError maps mmsync's own "attachments are not enabled" error to a
// real UI code instead of falling through to internal — the composer
// otherwise showed a generic "something went wrong" for a case the UI
// already has a proper localized message for.
func TestActionErrorMapsNoAttachments(t *testing.T) {
	assert.Equal(t, CodeAttachmentsDisabled, codeOf(actionError(mmsync.ErrNoAttachments)))
}
