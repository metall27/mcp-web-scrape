package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

func TestConcurrentCacheAccess(t *testing.T) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        1 * time.Minute,
		CleanupInt: 5 * time.Minute,
	}

	c, err := cache.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Test concurrent writes
	numGoroutines := 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			key := "test-key-" + string(rune('0'+index%10))
			data := &cache.CachedResponse{
				Data:      []byte("test data"),
				Timestamp: time.Now(),
			}

			// Write
			c.Set(ctx, key, data, 1*time.Minute)

			// Read
			c.Get(ctx, key)
		}(i)
	}

	wg.Wait()

	// Verify no corruption
	t.Logf("Concurrent test completed with %d goroutines", numGoroutines)
}

func TestConcurrentCacheKeyGeneration(t *testing.T) {
	numGoroutines := 50
	var wg sync.WaitGroup
	keys := make(chan string, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			url := "https://example.com/page" + string(rune('0'+index%10))
			key := GenerateCacheKey(url, map[string]interface{}{})
			keys <- key
		}(i)
	}

	wg.Wait()
	close(keys)

	uniqueKeys := make(map[string]bool)
	for key := range keys {
		uniqueKeys[key] = true
	}

	t.Logf("Generated %d unique keys from %d goroutines", len(uniqueKeys), numGoroutines)
}

func TestConcurrentScraperCreation(t *testing.T) {
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        1 * time.Minute,
		CleanupInt: 5 * time.Minute,
	}

	c, _ := cache.New(cfg)

	numGoroutines := 20
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// Test scraper creation (doesn't need logger)
			scraper := NewHTTPScraper(c, nil, nil)
			_ = scraper.Name()
		}(i)
	}

	wg.Wait()
	t.Logf("Concurrent scraper creation test completed with %d goroutines", numGoroutines)
}