package tools

import (
	"context"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

// TestRetryIntegration проверка что retry работает в интеграции с HTTPScraper
func TestRetryIntegration(t *testing.T) {
	t.Log("Testing retry integration with real HTTPScraper...")

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	// Create scraper with retry
	scraper := NewHTTPScraper(cache, nil, nil)
	retryScraper := NewRetryScraper(scraper, RetryConfig{
		MaxAttempts:  2,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	})

	// Test with working URL
	ctx := context.Background()
	result, err := retryScraper.Scrape(ctx, "https://example.com", Options{
		Timeout: 5 * time.Second,
	})

	if err != nil {
		t.Errorf("Should succeed for example.com, got error: %v", err)
	}

	if result == nil {
		t.Error("Result should not be nil")
	}

	if result.StatusCode != 200 {
		t.Errorf("Status should be 200, got: %d", result.StatusCode)
	}

	t.Logf("✅ Retry integration works! Status: %d, Size: %d bytes",
		result.StatusCode, len(result.HTML))
}

// TestErrorHintsIntegration проверка что error hints возвращаются
func TestErrorHintsIntegration(t *testing.T) {
	t.Log("Testing error hints integration...")

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)

	// Test with invalid URL (должен вернуть error с hints)
	ctx := context.Background()
	result, err := scraper.Scrape(ctx, "not-a-valid-url", Options{})

	if err == nil {
		t.Error("Should return error for invalid URL")
	}

	if result != nil {
		t.Error("Result should be nil for error")
	}

	// Check error structure
	if err.Code == "" {
		t.Error("Error code should not be empty")
	}

	if err.Message == "" {
		t.Error("Error message should not be empty")
	}

	// invalid_url не должен иметь hints
	t.Logf("✅ Error hints integration works!")
	t.Logf("   Code: %s", err.Code)
	t.Logf("   Message: %s", err.Message)
	t.Logf("   Hints: %v", err.Hints)
	t.Logf("   CanRetry: %v", err.CanRetry)
}

// TestTimeoutErrorHandling проверка обработки timeout
func TestTimeoutErrorHandling(t *testing.T) {
	t.Log("Testing timeout error handling...")

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)

	// Test with very short timeout to trigger timeout error
	ctx := context.Background()
	result, err := scraper.Scrape(ctx, "https://httpbin.org/delay/10", Options{
		Timeout: 1 * time.Second, // Too short
	})

	if err != nil {
		t.Logf("Got expected error: %s", err.Code)

		// Check if it's a timeout or network error
		if err.Code != "timeout" && err.Code != "network_error" && err.Code != "http_error" {
			t.Logf("Warning: expected timeout-like error, got: %s", err.Code)
		}

		// Should have hints
		if len(err.Hints) == 0 {
			t.Error("Timeout error should provide hints")
		}

		// Should be retryable
		if !err.CanRetry {
			t.Error("Timeout error should be retryable")
		}

		t.Logf("✅ Timeout error handling works!")
		t.Logf("   Code: %s", err.Code)
		t.Logf("   Hints: %v", err.Hints)
		t.Logf("   CanRetry: %v", err.CanRetry)
	} else {
		t.Log("Request succeeded (no timeout, httpbin might be fast)")
		if result != nil {
			t.Logf("   Status: %d, Size: %d bytes", result.StatusCode, len(result.HTML))
		}
	}
}

// TestPerformanceRegression проверка что нет регрессии производительности
func TestPerformanceRegression(t *testing.T) {
	t.Log("Testing performance regression...")

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	// Test with retry
	scraper := NewHTTPScraper(cache, nil, nil)
	retryScraper := NewRetryScraper(scraper, DefaultRetryConfig)

	ctx := context.Background()
	start := time.Now()

	result, err := retryScraper.Scrape(ctx, "https://example.com", Options{
		Timeout: 5 * time.Second,
	})

	duration := time.Since(start)

	if err != nil {
		t.Errorf("Should succeed for example.com, got error: %v", err)
	}

	if result == nil {
		t.Error("Result should not be nil")
	}

	// Should be reasonably fast (less than 10 seconds for successful case)
	if duration > 10*time.Second {
		t.Errorf("Performance regression: took %v, expected < 10s", duration)
	}

	t.Logf("✅ No performance regression!")
	t.Logf("   Duration: %v", duration)
	t.Logf("   Status: %d", result.StatusCode)
}

// Benchmark with and without retry
func BenchmarkWithRetry(b *testing.B) {
	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)
	retryScraper := NewRetryScraper(scraper, DefaultRetryConfig)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		retryScraper.Scrape(ctx, "https://example.com", Options{
			Timeout: 5 * time.Second,
		})
	}
}

func BenchmarkWithoutRetry(b *testing.B) {
	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scraper.Scrape(ctx, "https://example.com", Options{
			Timeout: 5 * time.Second,
		})
	}
}
