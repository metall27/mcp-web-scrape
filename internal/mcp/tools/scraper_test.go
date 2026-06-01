package tools

import (
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

func TestGenerateCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		params   map[string]interface{}
		wantPrefix bool // check if key starts with "scrape:"
	}{
		{
			name: "Simple URL",
			url:  "https://example.com",
			params: map[string]interface{}{},
			wantPrefix: true,
		},
		{
			name: "URL with custom parameters",
			url:  "https://example.com",
			params: map[string]interface{}{
				"user_agent": "CustomAgent",
			},
			wantPrefix: true,
		},
		{
			name: "Different URLs should have different keys",
			url:  "https://example.com/page2",
			params: map[string]interface{}{},
			wantPrefix: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := GenerateCacheKey(tt.url, tt.params)
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
	key1 := GenerateCacheKey("https://example.com", map[string]interface{}{})
	key2 := GenerateCacheKey("https://example.com", map[string]interface{}{})
	if key1 != key2 {
		t.Error("Same URL should produce same cache key")
	}

	// Test that different URLs produce different keys
	key3 := GenerateCacheKey("https://example.com/page1", map[string]interface{}{})
	key4 := GenerateCacheKey("https://example.com/page2", map[string]interface{}{})
	if key3 == key4 {
		t.Error("Different URLs should produce different cache keys")
	}
}

func TestGenerateCacheKeyJS(t *testing.T) {
	// Test JS cache key generation
	key1 := GenerateCacheKeyJS("https://example.com", map[string]interface{}{
		"wait_for": ".content",
		"wait_time": "3s",
	})

	key2 := GenerateCacheKeyJS("https://example.com", map[string]interface{}{
		"wait_for": ".header",
		"wait_time": "3s",
	})

	if key1 == key2 {
		t.Error("Different parameters should produce different cache keys")
	}

	if key1[:10] != "scrape_js:" {
		t.Errorf("JS cache key should start with 'scrape_js:', got %s", key1[:10])
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name: "Valid HTTP URL",
			url:  "http://example.com",
			wantErr: false,
		},
		{
			name: "Valid HTTPS URL",
			url:  "https://example.com",
			wantErr: false,
		},
		{
			name: "Invalid URL",
			url:  "not-a-url",
			wantErr: true,
		},
		{
			name: "Unsupported scheme",
			url:  "ftp://example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
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

func TestCacheKeyWithDifferentParameters(t *testing.T) {
	// Same URL, different parameters should produce different keys
	params1 := map[string]interface{}{
		"user_agent": "Agent1",
	}

	params2 := map[string]interface{}{
		"user_agent": "Agent2",
	}

	key1 := GenerateCacheKey("https://example.com", params1)
	key2 := GenerateCacheKey("https://example.com", params2)

	if key1 == key2 {
		t.Error("Different parameters should produce different cache keys")
	}
}

func TestHTTPScraperInterface(t *testing.T) {
	scraper := NewHTTPScraper(nil, nil, nil)

	if scraper.Name() != "HTTP" {
		t.Errorf("Expected name 'HTTP', got '%s'", scraper.Name())
	}

	if scraper.SupportsJS() {
		t.Error("HTTPScraper should not support JS")
	}

	if scraper.SupportsActions() {
		t.Error("HTTPScraper should not support actions")
	}
}

func TestChromeScraperInterface(t *testing.T) {
	scraper := NewChromeScraper(nil, nil, config.RAGConfig{}, config.BrowserConfig{}, nil, nil)

	if scraper.Name() != "Chrome" {
		t.Errorf("Expected name 'Chrome', got '%s'", scraper.Name())
	}

	if !scraper.SupportsJS() {
		t.Error("ChromeScraper should support JS")
	}

	if !scraper.SupportsActions() {
		t.Error("ChromeScraper should support actions")
	}
}

func TestUnifiedScraperInterface(t *testing.T) {
	httpScraper := NewHTTPScraper(nil, nil, nil)
	chromeScraper := NewChromeScraper(nil, nil, config.RAGConfig{}, nil, nil)

	unified := NewUnifiedScraper([]Scraper{httpScraper, chromeScraper})

	if unified.Name() != "Unified" {
		t.Errorf("Expected name 'Unified', got '%s'", unified.Name())
	}

	if !unified.SupportsJS() {
		t.Error("UnifiedScraper should support JS (has ChromeScraper)")
	}

	if !unified.SupportsActions() {
		t.Error("UnifiedScraper should support actions (has ChromeScraper)")
	}
}