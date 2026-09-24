package desktop

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		data map[string]any
		id   int64
		ch   string
		ok   bool
	}{
		{"linux keeps go values", map[string]any{"server_id": int64(3), "channel_id": "c1"}, 3, "c1", true},
		{"json number", map[string]any{"server_id": float64(4), "channel_id": "c2"}, 4, "c2", true},
		{"json.Number", map[string]any{"server_id": json.Number("5"), "channel_id": "c3"}, 5, "c3", true},
		{"string", map[string]any{"server_id": "6", "channel_id": "c4"}, 6, "c4", true},
		{"test notification", map[string]any{"target": "test", "id": "x"}, 0, "", false},
		{"no channel", map[string]any{"server_id": int64(3)}, 3, "", false},
		{"nil", nil, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ch, ok := clickTarget(tc.data)
			assert.Equal(t, tc.id, id)
			assert.Equal(t, tc.ch, ch)
			assert.Equal(t, tc.ok, ok)
		})
	}
}
