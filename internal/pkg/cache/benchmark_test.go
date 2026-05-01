package cache

import (
	"context"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

func BenchmarkCacheSetAndGet(b *testing.B) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, _ := New(cfg)
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
		Headers:   map[string]string{"content-type": "text/html"},
	}

	b.ResetTimer() // Reset timer to skip setup time
	for i := 0; i < b.N; i++ {
		key := "bench-key-" + string(rune('0'+i%10))
		cache.Set(ctx, key, data, 5*time.Minute)
		cache.Get(ctx, key)
	}
}

func BenchmarkGetTTLForContentType(b *testing.B) {
	cfg := config.CacheConfig{
		Enabled: true,
		TTL:     15 * time.Minute,
		TTLByType: map[string]time.Duration{
			"text/html":              5 * time.Minute,
			"application/json":       10 * time.Minute,
			"text/css":               1 * time.Hour,
			"application/javascript": 1 * time.Hour,
			"image/*":                1 * time.Hour,
		},
		CleanupInt: 10 * time.Minute,
	}

	cache, _ := New(cfg)

	contentTypes := []string{
		"text/html",
		"text/html; charset=utf-8",
		"application/json",
		"application/json; charset=utf-8",
		"text/css",
		"application/javascript",
		"image/png",
		"image/jpeg",
		"application/xml",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ct := contentTypes[i%len(contentTypes)]
		cache.GetTTLForContentType(ct)
	}
}

func BenchmarkCacheSet(b *testing.B) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, _ := New(cfg)
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "bench-key-" + string(rune('0'+i%10))
		cache.Set(ctx, key, data, 5*time.Minute)
	}
}

func BenchmarkCacheGet(b *testing.B) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, _ := New(cfg)
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
	}

	// Pre-populate cache
	for i := 0; i < 100; i++ {
		key := "bench-key-" + string(rune('0'+i%10))
		cache.Set(ctx, key, data, 5*time.Minute)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "bench-key-" + string(rune('0'+i%10))
		cache.Get(ctx, key)
	}
}

func BenchmarkCacheConcurrent(b *testing.B) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, _ := New(cfg)
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "bench-key-" + string(rune('0'+i%10))
			cache.Set(ctx, key, data, 5*time.Minute)
			cache.Get(ctx, key)
			i++
		}
	})
}
