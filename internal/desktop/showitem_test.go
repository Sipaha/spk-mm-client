package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileURIEscapesThePath(t *testing.T) {
	assert.Equal(t, "file:///home/u/%D0%97%D0%B0%D0%B3%D1%80%D1%83%D0%B7%D0%BA%D0%B8/a%20b%23%3F.pdf",
		fileURI("/home/u/Загрузки/a b#?.pdf"))
}

func TestShowItemAsksForTheFileURI(t *testing.T) {
	var got string
	err := showItem(context.Background(), "/dl/a.txt", func(_ context.Context, uri string) error {
		got = uri
		return nil
	}, time.Second)
	require.NoError(t, err)
	assert.Equal(t, "file:///dl/a.txt", got)

	boom := errors.New("org.freedesktop.DBus.Error.ServiceUnknown")
	err = showItem(context.Background(), "/dl/a.txt", func(context.Context, string) error { return boom }, time.Second)
	assert.ErrorIs(t, err, boom)
}

func TestShowItemGivesUpOnAHungBus(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	err := showItem(context.Background(), "/dl/a.txt", func(context.Context, string) error {
		<-release // ignores its context, like a hung connect
		return nil
	}, 50*time.Millisecond)
	assert.ErrorIs(t, err, errShowItemTimeout)
	assert.Less(t, time.Since(start), time.Second)
}
