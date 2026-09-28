package api

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/state"
)

func candidate(chType, chName, sender, msg string) state.NotifyCandidate {
	return state.NotifyCandidate{
		Post:        model.Post{ID: "p1", ChannelID: "c1", UserID: "u-bob", Message: msg},
		Channel:     model.Channel{ID: "c1", Type: chType},
		ChannelName: chName, SenderName: sender,
	}
}

func TestNotificationText(t *testing.T) {
	n := notificationFor(7, candidate(model.ChannelOpen, "Off-Topic", "bob", "hi  @alice\n\nsecond line"))
	assert.Equal(t, Notification{ID: "mm-7-c1", Title: "Off-Topic", Body: "bob: hi @alice second line", ServerID: 7, ChannelID: "c1"}, n)

	// A reply (with or without CRT) opens its thread; a thread's series is
	// folded apart from its channel's.
	r := candidate(model.ChannelOpen, "Off-Topic", "bob", "in the thread")
	r.Post.RootID = "root1"
	assert.Equal(t, Notification{ID: "mm-7-c1-root1", Title: "Off-Topic", Body: "bob: in the thread", ServerID: 7, ChannelID: "c1", RootID: "root1"},
		notificationFor(7, r))

	dm := notificationFor(7, candidate(model.ChannelDirect, "bob", "bob", "ping"))
	assert.Equal(t, "bob", dm.Title)
	assert.Equal(t, "ping", dm.Body, "a DM body has no sender prefix")

	long := notificationFor(1, candidate(model.ChannelOpen, "C", "bob", strings.Repeat("я", 300)))
	assert.Equal(t, 200, len([]rune(long.Body)))
	assert.True(t, strings.HasSuffix(long.Body, "…"))

	att := candidate(model.ChannelOpen, "C", "ci", "")
	att.Post.Props.Attachments = []model.Attachment{{Fallback: "Build #42 failed"}}
	assert.Equal(t, "ci: Build #42 failed", notificationFor(1, att).Body)

	file := candidate(model.ChannelOpen, "C", "bob", "")
	file.Post.Metadata = &model.PostMetadata{Files: []model.FileInfo{{Name: "report.pdf"}}}
	assert.Equal(t, "bob: 📎 report.pdf", notificationFor(1, file).Body)

	// The sender's name comes from state (override_username only where the
	// server allows it — TestNotifySenderFollowsTheUsernameOverride); the
	// props are not read again here.
	hook := candidate(model.ChannelOpen, "C", "Deploy Bot", "deployed")
	hook.Post.Props.FromWebhook, hook.Post.Props.OverrideUsername = true, "Deploy Bot"
	assert.Equal(t, "Deploy Bot: deployed", notificationFor(1, hook).Body)
	off := candidate(model.ChannelOpen, "C", "webhook-owner", "deployed")
	off.Post.Props.FromWebhook, off.Post.Props.OverrideUsername = true, "Deploy Bot"
	assert.Equal(t, "webhook-owner: deployed", notificationFor(1, off).Body, "EnablePostUsernameOverride off: state gave the account")
}

type collect struct {
	mu  sync.Mutex
	got []Notification
}

func (c *collect) add(n Notification) { c.mu.Lock(); c.got = append(c.got, n); c.mu.Unlock() }
func (c *collect) list() []Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Notification(nil), c.got...)
}

func TestNotifyQueueBurstsPerChannel(t *testing.T) {
	var c collect
	q := newNotifyQueue(80*time.Millisecond, c.add)
	defer q.close()
	q.push(Notification{ID: "a", Body: "a1"})
	require.Eventually(t, func() bool { return len(c.list()) == 1 }, time.Second, 5*time.Millisecond, "first one is immediate")
	q.push(Notification{ID: "a", Body: "a2"})
	q.push(Notification{ID: "a", Body: "a3"})
	q.push(Notification{ID: "a", Body: "a4"})
	q.push(Notification{ID: "b", Body: "b1"})
	require.Eventually(t, func() bool { return len(c.list()) == 3 }, time.Second, 5*time.Millisecond)
	got := c.list() // a1 and b1 are sent from goroutines: their order is not fixed
	assert.ElementsMatch(t, []string{"a1", "b1"}, []string{got[0].Body, got[1].Body})
	assert.Equal(t, "a4 (+2)", got[2].Body)

	time.Sleep(100 * time.Millisecond)
	q.push(Notification{ID: "b", Body: "b2"}) // b's window is over: immediate again
	require.Eventually(t, func() bool { return len(c.list()) == 4 }, time.Second, 5*time.Millisecond)
	assert.Equal(t, "b2", c.list()[3].Body)
}

func TestNotifyQueueSingleFollowerHasNoCounter(t *testing.T) {
	var c collect
	q := newNotifyQueue(30*time.Millisecond, c.add)
	defer q.close()
	q.push(Notification{ID: "a", Body: "one"})
	q.push(Notification{ID: "a", Body: "two"})
	require.Eventually(t, func() bool { return len(c.list()) == 2 }, time.Second, 5*time.Millisecond)
	assert.Equal(t, "two", c.list()[1].Body)
}
