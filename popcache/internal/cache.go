package internal

import (
	"log"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/yzp0n/ncdn/types"
)

type Key [32]byte

type Cache struct {
	cache *lru.Cache[Key, *types.Object]
}

func NewCache(cap int) *Cache {
	cache, err := lru.New[Key, *types.Object](256)
	if err != nil {
		log.Fatalf("Failed to create lru.Cache: %v", err)
	}
	return &Cache{
		cache: cache,
	}
}

func (c *Cache) Get(key Key) (*types.Object, bool) {
	obj, ok := c.cache.Get(key)
	if !ok {
		return nil, false
	}
	return obj.Clone(), true
}

func (c *Cache) Put(key Key, obj *types.Object) {
	c.cache.Add(key, obj)
}
