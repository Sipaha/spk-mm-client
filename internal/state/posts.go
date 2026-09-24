package state

// Pending is a locally created post awaiting server confirmation.
// Task 7 extends this file with the posts window logic.
type Pending struct {
	ID        string
	ChannelID string
	RootID    string
	Message   string
	CreateAt  int64
	Failed    bool
}

// seenSet is a bounded set used to de-duplicate WebSocket events already
// applied. Task 7 extends this file with the real implementation.
type seenSet struct {
	ids  map[string]struct{}
	ring []string
}

func newSeenSet(n int) seenSet {
	return seenSet{ids: map[string]struct{}{}, ring: make([]string, n)}
}
