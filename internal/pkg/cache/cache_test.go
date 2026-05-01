package cache

import (
	"context"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

func TestGetTTLForContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expectedTTL time.Duration
	}{
		{
			name:        "Plain HTML",
			contentType: "text/html",
			expectedTTL: 5 * time.Minute,
		},
		{
			name:        "HTML with charset",
			contentType: "text/html; charset=utf-8",
			expectedTTL: 5 * time.Minute,
		},
		{
			name:        "JSON",
			contentType: "application/json",
			expectedTTL: 10 * time.Minute,
		},
		{
			name:        "JSON with charset",
			contentType: "application/json; charset=utf-8",
			expectedTTL: 10 * time.Minute,
		},
		{
			name:        "CSS",
			contentType: "text/css",
			expectedTTL: 1 * time.Hour,
		},
		{
			name:        "JavaScript",
			contentType: "application/javascript",
			expectedTTL: 1 * time.Hour,
		},
		{
			name:        "PNG (wildcard match)",
			contentType: "image/png",
			expectedTTL: 1 * time.Hour,
		},
		{
			name:        "JPEG (wildcard match)",
			contentType: "image/jpeg",
			expectedTTL: 1 * time.Hour,
		},
		{
			name:        "Unknown type - default TTL",
			contentType: "application/xml",
			expectedTTL: 15 * time.Minute,
		},
		{
			name:        "Empty content type - default TTL",
			contentType: "",
			expectedTTL: 15 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			cache, err := New(cfg)
			if err != nil {
				t.Fatalf("Failed to create cache: %v", err)
			}

			ttl := cache.GetTTLForContentType(tt.contentType)
			if ttl != tt.expectedTTL {
				t.Errorf("GetTTLForContentType(%q) = %v, want %v", tt.contentType, ttl, tt.expectedTTL)
			}
		})
	}
}

func TestCacheSetAndGet(t *testing.T) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Test Set and Get
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
		Headers: map[string]string{
			"content-type": "text/html",
		},
	}

	err = cache.Set(ctx, "test-key", data, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to set cache: %v", err)
	}

	retrieved, found := cache.Get(ctx, "test-key")
	if !found {
		t.Fatal("Cache entry not found")
	}

	if string(retrieved.Data) != "test data" {
		t.Errorf("Expected data 'test data', got '%s'", string(retrieved.Data))
	}

	if retrieved.Headers["content-type"] != "text/html" {
		t.Errorf("Expected content-type 'text/html', got '%s'", retrieved.Headers["content-type"])
	}
}

func TestCacheDelete(t *testing.T) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Set a value
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
	}

	err = cache.Set(ctx, "test-key", data, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to set cache: %v", err)
	}

	// Delete it
	err = cache.Delete(ctx, "test-key")
	if err != nil {
		t.Fatalf("Failed to delete cache: %v", err)
	}

	// Verify it's gone
	_, found := cache.Get(ctx, "test-key")
	if found {
		t.Error("Cache entry should be deleted but still exists")
	}
}

func TestCacheClear(t *testing.T) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Set multiple values
	for i := 0; i < 5; i++ {
		data := &CachedResponse{
			Data:      []byte("test data"),
			Timestamp: time.Now(),
		}
		err = cache.Set(ctx, "test-key-"+string(rune('0'+i)), data, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to set cache: %v", err)
		}
	}

	// Clear all
	err = cache.Clear(ctx)
	if err != nil {
		t.Fatalf("Failed to clear cache: %v", err)
	}

	// Verify all are gone
	for i := 0; i < 5; i++ {
		_, found := cache.Get(ctx, "test-key-"+string(rune('0'+i)))
		if found {
			t.Error("Cache entry should be deleted but still exists")
		}
	}
}

func TestCacheDisabled(t *testing.T) {
	ctx := context.Background()
	cfg := config.CacheConfig{
		Enabled:    false,
		TTL:        5 * time.Minute,
		CleanupInt: 10 * time.Minute,
	}

	cache, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	if cache.IsEnabled() {
		t.Error("Cache should be disabled")
	}

	// Set should not fail but should not store
	data := &CachedResponse{
		Data:      []byte("test data"),
		Timestamp: time.Now(),
	}

	err = cache.Set(ctx, "test-key", data, 5*time.Minute)
	if err != nil {
		t.Fatalf("Set should not fail when cache is disabled: %v", err)
	}

	// Get should return not found
	_, found := cache.Get(ctx, "test-key")
	if found {
		t.Error("Cache should not store when disabled")
	}
}
