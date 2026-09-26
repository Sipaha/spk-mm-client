package mmsync

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

// Profiles restored from the snapshot are complete, so nothing is missing
// after a restart: without the one-shot refresh a picture changed while we
// were offline would keep its old version (and its immutable media) until
// the next live user_updated.
func TestKnownUsersAreRefreshedOnceAfterBootstrap(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar != "" }, "bob's profile never loaded")
	h.eventually(func() bool { return h.liveAt() > 0 }, "the live mark never reached the snapshot")
	h.stop()

	at := h.fake.SetPicture("bob") // while we are offline
	sinceCalls := len(h.fake.UsersSince())
	h.start()
	h.live()
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar == strconv.FormatInt(at, 10) },
		"a picture changed while offline was not picked up after the bootstrap")
	since := h.fake.UsersSince()[sinceCalls:]
	require.Len(t, since, 1, "one refresh per bootstrap")
	assert.Less(t, since[0], at, "asks for users updated since we were last live")
}
