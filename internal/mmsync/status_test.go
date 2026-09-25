package mmsync

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

const statusPath = "/api/v4/users/status/ids"

func dmItem(h *harness, id string) state.ChannelItem {
	for _, c := range h.w.State().Sidebar("").Categories {
		for _, it := range c.Channels {
			if it.ID == id {
				return it
			}
		}
	}
	return state.ChannelItem{}
}

func authorStatus(h *harness, ch, uid string) string {
	for _, p := range h.view(ch).Posts {
		if p.UserID == uid {
			return p.Status
		}
	}
	return ""
}

func (h *harness) statusPolls() int { return h.fake.Hits("POST", statusPath) }

// statusHarness: the periodic poll is out of the picture (1 h), so every
// poll the test sees comes from a trigger. pollsAtLive records the polls
// made by the time the worker first reported live.
func statusHarness(t *testing.T, o mmfake.Options) (*harness, *atomic.Int64) {
	h := newHarness(t, o)
	pollsAtLive := &atomic.Int64{}
	pollsAtLive.Store(-1)
	h.tune = func(c *Config) {
		c.StatusEvery = time.Hour
		prev := c.Hooks.Status
		c.Hooks.Status = func(id int64, s Status) {
			if s == StatusLive {
				pollsAtLive.CompareAndSwap(-1, int64(h.statusPolls()))
			}
			prev(id, s)
		}
	}
	return h, pollsAtLive
}

func TestStatusesArePolledOnLiveAndOnOpenChannel(t *testing.T) {
	h, pollsAtLive := statusHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Status == "online" }, "going live did not poll bob's status")
	assert.Equal(t, int64(0), pollsAtLive.Load(), "nothing polls before live")
	assert.Equal(t, 1, h.statusPolls())

	h.eventually(h.allLoaded, "prefetch")
	require.Empty(t, authorStatus(h, "c-town", "u-carol"), "carol is not a DM partner: unknown until her channel opens")
	before := h.statusPolls()
	for _, ch := range []string{"c-offtopic", "c-secret", "c-town", "c-offtopic", "c-town"} {
		h.w.OpenChannel(ch)
	}
	require.Eventually(t, func() bool { return authorStatus(h, "c-town", "u-carol") == "away" },
		time.Second, 20*time.Millisecond, "opening a channel polls its authors")
	time.Sleep(2 * statusDebounce)
	assert.Equal(t, before+1, h.statusPolls(), "a burst of switches costs one poll")

	at := h.fake.SetPicture("bob")
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar == strconv.FormatInt(at, 10) }, "user_updated did not bump the picture version")
}

func TestStatusesArePolledWhenTheOpenChannelLoads(t *testing.T) {
	h, _ := statusHarness(t, mmfake.Options{})
	h.fake.SetLatency("/channels/c-offtopic/posts", time.Second)
	h.start()
	h.live()
	h.w.OpenChannel("c-offtopic") // its window is still on the way
	h.eventually(func() bool { return h.statusPolls() >= 1 }, "no poll after live")
	require.Empty(t, authorStatus(h, "c-offtopic", "u-carol"))
	require.Eventually(t, func() bool { return authorStatus(h, "c-offtopic", "u-carol") == "away" },
		3*time.Second, 20*time.Millisecond, "authors of a window loaded after the open are polled")

	h.fake.SetStatus("carol", "dnd")
	h.eventually(h.allLoaded, "prefetch")
	polls := h.statusPolls()
	require.NoError(t, h.w.LoadOlder(context.Background(), "c-offtopic"))
	require.NoError(t, h.w.LoadOlder(context.Background(), "c-town")) // not the open channel: no poll
	require.Eventually(t, func() bool { return authorStatus(h, "c-offtopic", "u-carol") == "dnd" },
		time.Second, 20*time.Millisecond, "loading older history polls the open channel's authors")
	time.Sleep(2 * statusDebounce)
	assert.Equal(t, polls+1, h.statusPolls())
}
