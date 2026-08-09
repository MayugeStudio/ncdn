package internal

import (
	"log"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/yzp0n/ncdn/types"
)

type Key [32]byte

type Cache struct {
	cache *lru.Cache[Key, *types.CacheEntry]
}

func NewCache(cap int) *Cache {
	cache, err := lru.New[Key, *types.CacheEntry](256)
	if err != nil {
		log.Fatalf("Failed to create lru.Cache: %v", err)
	}
	return &Cache{
		cache: cache,
	}
}

func (c *Cache) Get(key Key) (*types.CacheEntry, bool) {
	ce, ok := c.cache.Get(key)
	if !ok {
		return nil, false
	}
	return ce.Clone(), true
}

func (c *Cache) Put(key Key, ce *types.CacheEntry) {
	c.cache.Add(key, ce)
}
