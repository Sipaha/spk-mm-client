package state

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/stretchr/testify/assert"
)

func TestNotificationReceiptsFollowReadWatermarks(t *testing.T) {
	s := newFixture()
	first := s.WatchNotification(mkPost("p1", "off", "u2", 100))
	newer := s.WatchNotification(mkPost("p2", "off", "u2", 200))
	other := s.WatchNotification(mkPost("p3", "town", "u2", 100))
	assert.False(t, first.Read())
	s.ViewedLocally("off", 150)
	assert.True(t, first.Read())
	assert.False(t, newer.Read(), "a later unread post must keep its notification")
	assert.False(t, other.Read())
	s.ApplyEvent(ws.Event{Type: "multiple_channels_viewed", Data: json.RawMessage(`{"channel_times":{"off":200}}`)})
	assert.True(t, newer.Read(), "read in another client")
	s.SetUnread(model.ChannelUnreadAt{ChannelID: "off", LastViewedAt: 1})
	assert.True(t, first.Read(), "mark unread must not resurrect a dismissed notification")
	s.mu.Lock()
	s.forgetChannelLocked("town")
	s.mu.Unlock()
	assert.True(t, other.Read())
}

func TestCRTNotificationReceiptsNeedTheirOwnThreadRead(t *testing.T) {
	s := crtCounts(nil)
	post := mkPost("reply", "off", "u2", 100)
	post.RootID = "root"
	r := s.WatchNotification(post)
	post.RootID = "other-root"
	other := s.WatchNotification(post)
	s.ViewedLocally("off", 200)
	assert.False(t, r.Read(), "a channel view does not read collapsed replies")
	eff := s.ApplyEvent(ws.Event{Type: "thread_read_changed", Broadcast: ws.Broadcast{TeamID: "t1", UserID: "u1"},
		Data: json.RawMessage(`{"thread_id":"root","channel_id":"off","timestamp":100}`)})
	assert.True(t, r.Read())
	assert.True(t, eff.Badge, "a read without mentions still reconciles notifications")
	assert.False(t, other.Read())
	s.ApplyEvent(ws.Event{Type: "thread_read_changed", Broadcast: ws.Broadcast{TeamID: "t1", UserID: "u1"},
		Data: json.RawMessage(`{"timestamp":100}`)})
	assert.True(t, other.Read(), "mark all threads read")
}

func TestNotificationWatchesAreBoundedAndBootstrapReadsChannels(t *testing.T) {
	s := crtCounts(nil)
	var first *NotificationReceipt
	for i := range 513 {
		p := mkPost(fmt.Sprint(i), "off", "u2", int64(100+i))
		p.RootID = fmt.Sprint(i)
		r := s.WatchNotification(p)
		if i == 0 {
			first = r
		}
	}
	assert.Len(t, s.notificationReads, 512)
	assert.True(t, first.Read(), "oldest group is retired at the cap")
	r := s.WatchNotification(mkPost("root-post", "town", "u2", 50))
	b := fixture()
	for i := range b.Members {
		b.Members[i].LastViewedAt = 100
	}
	s.Bootstrap(b)
	assert.True(t, r.Read())
}

func TestLocalThreadReadRetractsOnlyPostsThroughItsTimestamp(t *testing.T) {
	s := crtCounts(nil)
	s.OpenThread("off", "root")
	p := mkPost("reply", "off", "u2", 100)
	p.RootID = "root"
	first := s.WatchNotification(p)
	p.CreateAt = 200
	newer := s.WatchNotification(p)
	s.ThreadReadDone("root", s.openRead.opening, 100)
	assert.True(t, first.Read())
	assert.False(t, newer.Read())
}

func TestNotificationReceiptsFollowCurrentThreadModeAfterSwitch(t *testing.T) {
	for _, initiallyCRT := range []bool{false, true} {
		t.Run(fmt.Sprint(initiallyCRT), func(t *testing.T) {
			s := newFixture()
			if initiallyCRT {
				s = crtCounts(nil)
			}
			p := mkPost("reply", "off", "u2", 100)
			p.RootID = "root"
			r := s.WatchNotification(p)
			b := fixture()
			if !initiallyCRT {
				b.Config.CollapsedThreads = "always_on"
			}
			s.Bootstrap(b)
			s.ViewedLocally("off", 200)
			if initiallyCRT {
				assert.True(t, r.Read(), "after CRT is disabled, a channel view reads its replies")
			} else {
				assert.False(t, r.Read(), "after CRT is enabled, replies still need a thread read")
				s.OpenThread("off", "root")
				s.ThreadReadDone("root", s.openRead.opening, 100)
				assert.True(t, r.Read())
			}
		})
	}
}
