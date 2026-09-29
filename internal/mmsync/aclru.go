package mmsync

import (
	"container/list"
	"sync"
	"time"
)

// The composer autocomplete's cache of server answers: a small LRU per
// worker (so per server and per signed-in user — a new sign-in is a new
// worker; Run clears it on the way out), bounded in entries and age.
const (
	acCacheSize = 50
	acCacheTTL  = 60 * time.Second
)

type acEntry struct {
	key string
	at  time.Time
	val any
}

type acCache struct {
	mu  sync.Mutex
	now func() time.Time
	ll  *list.List // most recent first
	idx map[string]*list.Element
}

func newACCache(now func() time.Time) *acCache {
	return &acCache{now: now, ll: list.New(), idx: map[string]*list.Element{}}
}

func (c *acCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.idx[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*acEntry)
	if c.now().Sub(e.at) > acCacheTTL {
		c.ll.Remove(el)
		delete(c.idx, key)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *acCache) put(key string, val any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[key]; ok {
		el.Value = &acEntry{key: key, at: c.now(), val: val}
		c.ll.MoveToFront(el)
		return
	}
	c.idx[key] = c.ll.PushFront(&acEntry{key: key, at: c.now(), val: val})
	for c.ll.Len() > acCacheSize {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.idx, last.Value.(*acEntry).key)
	}
}

func (c *acCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.idx = map[string]*list.Element{}
}

func (c *acCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
