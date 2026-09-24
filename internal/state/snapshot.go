package state

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/store"
)

// SnapshotVersion changes whenever the snapshot format does; an old
// snapshot is then discarded and the server is synced from scratch.
const SnapshotVersion = 1

var (
	ErrNoSnapshot      = errors.New("state: no snapshot")
	ErrSnapshotVersion = errors.New("state: snapshot version mismatch")
)

const (
	kindMeta  = "meta"
	kindLive  = "live"
	kindChan  = "chan"
	kindPosts = "posts"
	kindUser  = "user"
)

// dirtySet tracks which parts of the server's state changed since the last
// snapshot was persisted.
type dirtySet struct {
	meta, live                    bool
	chans, posts, users, delChans map[string]bool
}

func newDirtySet() dirtySet {
	return dirtySet{chans: map[string]bool{}, posts: map[string]bool{}, users: map[string]bool{}, delChans: map[string]bool{}}
}

func (d *dirtySet) dropChan(id string) {
	delete(d.chans, id)
	delete(d.posts, id)
	d.delChans[id] = true
}

type metaSnap struct {
	Version    int                                `json:"version"`
	Me         model.User                         `json:"me"`
	Status     model.Status                       `json:"status"`
	Config     Config                             `json:"config"`
	Prefs      []model.Preference                 `json:"prefs"`
	Teams      []model.Team                       `json:"teams"`
	Categories map[string]model.OrderedCategories `json:"categories"`
	Nav        Nav                                `json:"nav"`
}

type chanSnap struct {
	Info   model.Channel       `json:"info"`
	Member model.ChannelMember `json:"member"`
	Draft  string              `json:"draft,omitempty"`
}

func (s *Server) SetLiveAt(ms int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveAt = ms
	s.dirty.live = true
}

func (s *Server) HasData() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.me.ID != ""
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("snapshot encode", "err", err) // model types always encode
	}
	return b
}

// TakeSnapshot returns what changed since the previous call and resets the
// dirty set. Encoding happens under the lock — it is cheap next to the disk
// write, which the caller does after releasing it.
func (s *Server) TakeSnapshot() (put []store.CacheEntry, del []store.CacheKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dirty
	s.dirty = newDirtySet()
	if d.meta {
		prefs := make([]model.Preference, 0, len(s.prefs))
		for k, v := range s.prefs {
			prefs = append(prefs, model.Preference{UserID: s.me.ID, Category: k.cat, Name: k.name, Value: v})
		}
		put = append(put, store.CacheEntry{Kind: kindMeta, Key: "server", Data: mustJSON(metaSnap{
			Version: SnapshotVersion, Me: s.me, Status: s.status, Config: s.cfg, Prefs: prefs,
			Teams: s.teams, Categories: s.cats, Nav: s.nav,
		})})
	}
	if d.live {
		put = append(put, store.CacheEntry{Kind: kindLive, Key: "at", Data: mustJSON(s.liveAt)})
	}
	for id := range d.chans {
		if ch := s.chans[id]; ch != nil {
			put = append(put, store.CacheEntry{Kind: kindChan, Key: id, Data: mustJSON(chanSnap{Info: ch.Info, Member: ch.Member, Draft: s.drafts[id]})})
		}
	}
	for id := range d.posts {
		if ch := s.chans[id]; ch != nil && ch.Win.Loaded {
			put = append(put, store.CacheEntry{Kind: kindPosts, Key: id, Data: mustJSON(ch.Win)})
		}
	}
	for id := range d.users {
		if u, ok := s.users[id]; ok {
			put = append(put, store.CacheEntry{Kind: kindUser, Key: id, Data: mustJSON(u)})
		}
	}
	for id := range d.delChans {
		if s.chans[id] == nil {
			del = append(del, store.CacheKey{Kind: kindChan, Key: id}, store.CacheKey{Kind: kindPosts, Key: id})
		}
	}
	return put, del
}

// Restore loads a snapshot into an empty Server (cold start). Every window
// comes back stale: the worker catches it up with posts?since=.
func (s *Server) Restore(entries []store.CacheEntry) error {
	var meta *metaSnap
	for _, e := range entries {
		if e.Kind == kindMeta && e.Key == "server" {
			var m metaSnap
			if err := json.Unmarshal(e.Data, &m); err != nil {
				return errors.Join(ErrSnapshotVersion, err)
			}
			meta = &m
		}
	}
	if meta == nil {
		return ErrNoSnapshot
	}
	if meta.Version != SnapshotVersion {
		return ErrSnapshotVersion
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.me, s.status, s.cfg, s.teams, s.cats = meta.Me, meta.Status, meta.Config, meta.Teams, meta.Categories
	if s.cats == nil {
		s.cats = map[string]model.OrderedCategories{}
	}
	s.nav = meta.Nav
	if s.nav.Channel == nil {
		s.nav.Channel = map[string]string{}
	}
	for _, p := range meta.Prefs {
		s.prefs[prefKey{p.Category, p.Name}] = p.Value
	}
	s.users[s.me.ID] = s.me
	var liveAt int64
	wins := map[string]Window{}
	for _, e := range entries {
		switch e.Kind {
		case kindLive:
			_ = json.Unmarshal(e.Data, &liveAt)
		case kindChan:
			var c chanSnap
			if json.Unmarshal(e.Data, &c) == nil {
				s.chans[e.Key] = &Chan{Info: c.Info, Member: c.Member}
				if c.Draft != "" {
					s.drafts[e.Key] = c.Draft
				}
			}
		case kindPosts:
			var w Window
			if json.Unmarshal(e.Data, &w) == nil {
				wins[e.Key] = w
			}
		case kindUser:
			var u model.User
			if json.Unmarshal(e.Data, &u) == nil {
				s.users[u.ID] = u
			}
		}
	}
	s.liveAt = liveAt
	for id, w := range wins {
		ch := s.chans[id]
		if ch == nil {
			continue
		}
		if !w.Stale {
			w.SyncedAt = max(w.SyncedAt, liveAt)
		}
		w.Loaded, w.Stale, w.GapAfter = true, true, ""
		if n := len(w.Posts); n > 0 {
			w.GapAfter = w.Posts[n-1].ID
		}
		ch.Win = w
		for _, p := range w.Posts {
			s.seen.add(p.ID)
		}
	}
	return nil
}
