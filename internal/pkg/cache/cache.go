package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"sync"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/patrickmn/go-cache"
)

type Cache struct {
	client *cache.Cache
	mu     sync.RWMutex
}

type CachedResponse struct {
	Data      []byte
	Timestamp time.Time
	Headers   map[string]string
}

func New(cfg config.CacheConfig) (*Cache, error) {
	if !cfg.Enabled {
		return &Cache{client: nil}, nil
	}

	c := &Cache{
		client: cache.New(cfg.TTL, cfg.CleanupInt),
	}

	return c, nil
}

func (c *Cache) Get(ctx context.Context, key string) (*CachedResponse, bool) {
	if c.client == nil {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if val, found := c.client.Get(key); found {
		if cached, ok := val.(*CachedResponse); ok {
			return cached, true
		}
	}

	return nil, false
}

func (c *Cache) Set(ctx context.Context, key string, data *CachedResponse, ttl time.Duration) error {
	if c.client == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.client.Set(key, data, ttl)
	return nil
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	if c.client == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.client.Delete(key)
	return nil
}

func (c *Cache) Clear(ctx context.Context) error {
	if c.client == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.client.Flush()
	return nil
}

func (c *Cache) IsEnabled() bool {
	return c.client != nil
}

// Serialize converts CachedResponse to bytes for storage
func (cr *CachedResponse) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(cr); err != nil {
		return nil, fmt.Errorf("failed to serialize: %w", err)
	}

	return buf.Bytes(), nil
}

// Deserialize creates CachedResponse from bytes
func DeserializeCachedResponse(data []byte) (*CachedResponse, error) {
	var cr CachedResponse
	buf := bytes.NewReader(data)
	dec := gob.NewDecoder(buf)

	if err := dec.Decode(&cr); err != nil {
		return nil, fmt.Errorf("failed to deserialize: %w", err)
	}

	return &cr, nil
}

func init() {
	// Register CachedResponse type for gob encoding
	gob.Register(&CachedResponse{})
}
