package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Failed to load default config: %v", err)
	}

	// Check server defaults
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Expected default host '0.0.0.0', got '%s'", cfg.Server.Host)
	}

	if cfg.Server.Port != 8192 {
		t.Errorf("Expected default port 8192, got %d", cfg.Server.Port)
	}

	// Check cache defaults
	if !cfg.Cache.Enabled {
		t.Error("Expected cache to be enabled by default")
	}

	if cfg.Cache.TTL != 15*time.Minute {
		t.Errorf("Expected default TTL 15m, got %v", cfg.Cache.TTL)
	}

	expectedTTL := map[string]time.Duration{
		"text/html":              5 * time.Minute,
		"application/json":       10 * time.Minute,
		"text/css":               1 * time.Hour,
		"application/javascript": 1 * time.Hour,
		"image/*":                1 * time.Hour,
	}

	for key, expectedDuration := range expectedTTL {
		actualDuration, exists := cfg.Cache.TTLByType[key]
		if !exists {
			t.Errorf("Expected TTL for '%s' to exist", key)
		} else if actualDuration != expectedDuration {
			t.Errorf("Expected TTL for '%s' to be %v, got %v", key, expectedDuration, actualDuration)
		}
	}

	// Check browser defaults
	if !cfg.Browser.Enabled {
		t.Error("Expected browser to be enabled by default")
	}

	if cfg.Browser.Timeout != 30*time.Second {
		t.Errorf("Expected default browser timeout 30s, got %v", cfg.Browser.Timeout)
	}

	// Check search defaults
	if cfg.Search.Provider != "duckduckgo" {
		t.Errorf("Expected default search provider 'duckduckgo', got '%s'", cfg.Search.Provider)
	}

	// Check rate limit defaults
	if !cfg.RateLimit.Enabled {
		t.Error("Expected rate limit to be enabled by default")
	}

	if cfg.RateLimit.RequestsPerSecond != 10.0 {
		t.Errorf("Expected default rate limit 10.0 req/s, got %f", cfg.RateLimit.RequestsPerSecond)
	}
}

func TestCacheConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config CacheConfig
		valid  bool
	}{
		{
			name: "Valid cache config",
			config: CacheConfig{
				Enabled:    true,
				TTL:        5 * time.Minute,
				CleanupInt: 10 * time.Minute,
			},
			valid: true,
		},
		{
			name: "Disabled cache",
			config: CacheConfig{
				Enabled:    false,
				TTL:        0,
				CleanupInt: 0,
			},
			valid: true,
		},
		{
			name: "Cache with TTL by type",
			config: CacheConfig{
				Enabled: true,
				TTL:     15 * time.Minute,
				TTLByType: map[string]time.Duration{
					"text/html": 5 * time.Minute,
				},
				CleanupInt: 10 * time.Minute,
			},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For now, we just verify the config can be created
			// Add validation logic if needed in the future
			if tt.config.Enabled && tt.config.TTL == 0 && len(tt.config.TTLByType) == 0 {
				t.Error("Enabled cache should have either default TTL or TTL by type")
			}
		})
	}
}
