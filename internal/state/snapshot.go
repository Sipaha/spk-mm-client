package state

// dirtySet tracks which parts of the server's state changed since the last
// snapshot was persisted. Task 8 extends this file with the real snapshot
// writer.
type dirtySet struct {
	meta     bool
	chans    map[string]bool
	posts    map[string]bool
	users    map[string]bool
	delChans map[string]bool
}

func newDirtySet() dirtySet {
	return dirtySet{chans: map[string]bool{}, posts: map[string]bool{}, users: map[string]bool{}, delChans: map[string]bool{}}
}

func (d *dirtySet) dropChan(id string) {
	delete(d.chans, id)
	delete(d.posts, id)
	d.delChans[id] = true
}
