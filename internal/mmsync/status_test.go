package mmsync

import (
	"strconv"
	"testing"
	"time"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

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

func TestStatusesArePolledForDMPartnersAndOpenChannelAuthors(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.tune = func(c *Config) { c.StatusEvery = 100 * time.Millisecond }
	h.start()
	h.live()
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Status == "online" }, "bob's status never arrived")
	h.fake.SetStatus("bob", "dnd")
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Status == "dnd" }, "the poll did not pick up the change")

	h.eventually(h.allLoaded, "prefetch")
	h.w.OpenChannel("c-town")
	h.eventually(func() bool {
		for _, p := range h.view("c-town").Posts {
			if p.UserID == "u-carol" && p.Status == "away" {
				return true
			}
		}
		return false
	}, "authors of the open channel are polled")

	at := h.fake.SetPicture("bob")
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar == strconv.FormatInt(at, 10) }, "user_updated did not bump the picture version")
}
