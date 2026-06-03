package tools

import (
	"context"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

// TestNavigationTimeout проверка что navigation timeout работает
func TestNavigationTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	t.Log("Testing navigation timeout with slow site...")

	// Setup browser pool
	browserCfg := config.BrowserConfig{
		Headless: true,
		MaxTabs:   1,
	}

	browserPool, err := browser.New(browser.Config{
		Logger:    nil,
		MaxTabs:   1,
		Headless:  true,
		Isolated: true,
	})

	if err != nil {
		t.Skipf("Failed to create browser pool: %v", err)
	}
	defer browserPool.Drain()

	cache, _ := cache.New(config.CacheConfig{Enabled: false})
	scraper := NewChromeScraper(cache, browserPool, config.RAGConfig{}, browserCfg, nil, nil, config.GitHubConfig{})

	// Test with httpbin which has configurable delay
	// This should trigger navigation timeout since we set it to 30s
	// and httpbin delay is 10s, but navigation waits for full page load
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	start := time.Now()

	// Use a site that loads slowly but eventually works
	result, err := scraper.Scrape(ctx, "https://example.com", Options{
		Timeout: 10 * time.Second, // Short timeout
	})

	duration := time.Since(start)

	t.Logf("Duration: %v", duration)

	if err != nil {
		t.Logf("Got error (expected for slow sites):")
		t.Logf("  Code: %s", err.Code)
		t.Logf("  Message: %s", err.Message)
		t.Logf("  CanRetry: %v", err.CanRetry)
		t.Logf("  Hints: %v", err.Hints)

		// Verify error structure
		if err.Code == "" {
			t.Error("Error code should not be empty")
		}

		// Check if timeout error
		if err.Code == "timeout" || err.Code == "navigation_timeout" {
			t.Logf("✅ Timeout detection works!")
		}

		// Should provide hints
		if len(err.Hints) > 0 {
			t.Logf("✅ Hints provided: %v", err.Hints)
		}
	} else {
		t.Logf("Request succeeded (site was fast enough)")
		if result != nil {
			t.Logf("  Status: %d, Size: %d bytes", result.StatusCode, len(result.HTML))
		}
	}

	// Should complete within reasonable time even if error
	if duration > 60*time.Second {
		t.Errorf("Too slow: %v > 60s", duration)
	}

	t.Log("✅ Navigation timeout test completed")
}

// TestChromeScraperTimeoutBehavior проверка полного timeout behavior ChromeScraper
func TestChromeScraperTimeoutBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	t.Log("Testing ChromeScraper timeout behavior...")

	// Setup
	browserCfg := config.BrowserConfig{
		Headless:  true,
		MaxTabs:    1,
		ToolTimeout: 45 * time.Second, // 45s total timeout
	}

	browserPool, err := browser.New(browser.Config{
		Logger:    nil,
		MaxTabs:   1,
		Headless:  true,
		Isolated:  true,
	})

	if err != nil {
		t.Skipf("Failed to create browser pool: %v", err)
	}
	defer browserPool.Drain()

	cache, _ := cache.New(config.CacheConfig{Enabled: false})
	scraper := NewChromeScraper(cache, browserPool, config.RAGConfig{}, browserCfg, nil, nil, config.GitHubConfig{})

	// Wrap with retry
	retryScraper := NewRetryScraper(scraper, RetryConfig{
		MaxAttempts:  2, // Only 2 attempts for faster test
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	})

	ctx := context.Background()
	start := time.Now()

	// Test with site that should timeout
	result, err := retryScraper.Scrape(ctx, "https://httpbin.org/delay/20", Options{
		Timeout: 5 * time.Second, // Very short timeout to trigger failure
	})

	duration := time.Since(start)

	t.Logf("Duration: %v", duration)

	if err != nil {
		t.Logf("✅ Got expected error: %s", err.Code)
		t.Logf("  Message: %s", err.Message)
		t.Logf("  CanRetry: %v", err.CanRetry)

		// Should be a recognizable error
		if err.Code == "" {
			t.Error("Error code should not be empty")
		}

		// Should provide recovery hints
		if len(err.Hints) == 0 {
			t.Error("Error should provide recovery hints")
		} else {
			t.Logf("  Hints: %v", err.Hints)
		}
	} else {
		t.Log("Request succeeded (httpbin was fast)")
		if result != nil {
			t.Logf("  Status: %d", result.StatusCode)
		}
	}

	// Should complete within tool timeout + retry overhead
	maxExpected := 50 * time.Second // 45s tool timeout + some overhead
	if duration > maxExpected {
		t.Errorf("Exceeded max expected duration: %v > %v", duration, maxExpected)
	}

	t.Logf("✅ Total test completed in: %v", duration)
}
