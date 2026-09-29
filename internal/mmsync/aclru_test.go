package mmsync

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAutocompleteCacheIsBoundedLRUWithTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newACCache(func() time.Time { return now })
	for i := range acCacheSize + 10 {
		c.put(fmt.Sprint(i), i)
	}
	assert.Equal(t, acCacheSize, c.len(), "bounded")
	_, ok := c.get("0")
	assert.False(t, ok, "the least recently used went first")
	v, ok := c.get("10")
	assert.True(t, ok)
	assert.Equal(t, 10, v)

	c.put("new", 1) // evicts 11: 10 was just used
	_, ok = c.get("11")
	assert.False(t, ok)
	_, ok = c.get("10")
	assert.True(t, ok)

	now = now.Add(acCacheTTL + time.Second)
	_, ok = c.get("10")
	assert.False(t, ok, "expired")
	assert.Less(t, c.len(), acCacheSize, "an expired entry is dropped when met")

	c.put("x", 1)
	c.clear()
	assert.Equal(t, 0, c.len())
	_, ok = c.get("x")
	assert.False(t, ok)
}
