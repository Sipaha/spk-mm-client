package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func TestCustomEmojiNamesAndEvents(t *testing.T) {
	s := newFixture()
	assert.False(t, s.CustomEmojiEnabled())
	b := fixture()
	b.Config.CustomEmoji = true
	s.Bootstrap(b)
	assert.True(t, s.CustomEmojiEnabled())

	s.SetCustomEmoji([]model.Emoji{{ID: "e1", Name: "parrot"}, {ID: "e2", Name: "gone", DeleteAt: 5}})
	id, ok := s.CustomEmojiID("parrot")
	assert.True(t, ok)
	assert.Equal(t, "e1", id)
	_, ok = s.CustomEmojiID("gone")
	assert.False(t, ok, "deleted emoji are not offered")

	d, _ := json.Marshal(map[string]any{"emoji": `{"id":"e3","name":"shipit"}`})
	s.ApplyEvent(ws.Event{Type: "emoji_added", Data: d})
	assert.Equal(t, []string{"parrot", "shipit"}, s.CustomEmojiNames())
}
