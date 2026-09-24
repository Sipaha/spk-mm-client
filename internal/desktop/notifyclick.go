package desktop

import (
	"encoding/json"
	"strconv"
)

// clickTarget reads the channel a clicked chat notification points to. On
// Linux the Data map comes back as the Go values we sent; other platforms
// round-trip it through JSON (float64) — so ids are parsed leniently.
func clickTarget(data map[string]any) (serverID int64, channelID string, ok bool) {
	channelID, _ = data["channel_id"].(string)
	switch v := data["server_id"].(type) {
	case int64:
		serverID = v
	case int:
		serverID = int64(v)
	case float64:
		serverID = int64(v)
	case json.Number:
		serverID, _ = v.Int64()
	case string:
		serverID, _ = strconv.ParseInt(v, 10, 64)
	}
	return serverID, channelID, serverID > 0 && channelID != ""
}
