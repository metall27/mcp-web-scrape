package tools

import (
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

func TestGetCacheKey(t *testing.T) {
	tool := &ScrapeTool{
		cache: &cache.Cache{},
	}

	tests := []struct {
		name     string
		url      string
		args     map[string]interface{}
		wantPrefix bool // check if key starts with "scrape:"
	}{
		{
			name: "Simple URL",
			url:  "https://example.com",
			args: map[string]interface{}{},
			wantPrefix: true,
		},
		{
			name: "URL with custom headers",
			url:  "https://example.com",
			args: map[string]interface{}{
				"headers": map[string]interface{}{
					"Authorization": "Bearer token123",
				},
			},
			wantPrefix: true,
		},
		{
			name: "Different URLs should have different keys",
			url:  "https://example.com/page2",
			args: map[string]interface{}{},
			wantPrefix: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tool.getCacheKey(tt.url, tt.args)
			if len(key) == 0 {
				t.Error("Cache key should not be empty")
			}
			if tt.wantPrefix && len(key) < 7 {
				t.Errorf("Cache key should start with 'scrape:', got %s (too short)", key)
			}
			if tt.wantPrefix && key[:7] != "scrape:" {
				t.Errorf("Cache key should start with 'scrape:', got %s", key[:7])
			}
		})
	}

	// Test that same URL produces same key
	key1 := tool.getCacheKey("https://example.com", map[string]interface{}{})
	key2 := tool.getCacheKey("https://example.com", map[string]interface{}{})
	if key1 != key2 {
		t.Error("Same URL should produce same cache key")
	}

	// Test that different URLs produce different keys
	key3 := tool.getCacheKey("https://example.com/page1", map[string]interface{}{})
	key4 := tool.getCacheKey("https://example.com/page2", map[string]interface{}{})
	if key3 == key4 {
		t.Error("Different URLs should produce different cache keys")
	}
}

func TestCacheIntegration(t *testing.T) {
	// Create a cache with short TTL for testing
	cfg := config.CacheConfig{
		Enabled:    true,
		TTL:        1 * time.Minute,
		TTLByType: map[string]time.Duration{
			"text/html": 30 * time.Second,
		},
		CleanupInt: 5 * time.Minute,
	}

	c, err := cache.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Test GetTTLForContentType
	htmlTTL := c.GetTTLForContentType("text/html")
	if htmlTTL != 30*time.Second {
		t.Errorf("Expected HTML TTL 30s, got %v", htmlTTL)
	}

	jsonTTL := c.GetTTLForContentType("application/json")
	if jsonTTL != 1*time.Minute {
		t.Errorf("Expected JSON TTL 1m, got %v", jsonTTL)
	}

	// Test with charset parameter
	htmlWithCharset := c.GetTTLForContentType("text/html; charset=utf-8")
	if htmlWithCharset != 30*time.Second {
		t.Errorf("Expected HTML with charset TTL 30s, got %v", htmlWithCharset)
	}
}

func TestCacheKeyWithHeaders(t *testing.T) {
	tool := &ScrapeTool{
		cache: &cache.Cache{},
	}

	// Same URL, different headers should produce different keys
	args1 := map[string]interface{}{
		"headers": map[string]interface{}{
			"Authorization": "Bearer token1",
		},
	}

	args2 := map[string]interface{}{
		"headers": map[string]interface{}{
			"Authorization": "Bearer token2",
		},
	}

	key1 := tool.getCacheKey("https://example.com", args1)
	key2 := tool.getCacheKey("https://example.com", args2)

	if key1 == key2 {
		t.Error("Different headers should produce different cache keys")
	}
}
