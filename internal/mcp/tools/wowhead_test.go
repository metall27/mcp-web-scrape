package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

// TestWowheadScraping тест скрапинга wowhead.com
// Запуск: go test -v -run TestWowheadScraping ./internal/mcp/tools
func TestWowheadScraping(t *testing.T) {
	// Setup
	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)

	// Test URL
	url := "https://www.wowhead.com/tbc/quest=1947/journey-to-the-marsh"

	t.Log("Testing URL:", url)

	// Create context with timeout (всего 30 секунд на тест)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Scrape
	result, err := scraper.Scrape(ctx, url, Options{
		Timeout:   10 * time.Second,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	})

	// Check result
	if err != nil {
		var scrapeErr *ScrapeError
		if errors.As(err, &scrapeErr) && scrapeErr != nil {
			t.Logf("❌ Scrape failed with error:")
			t.Logf("   Code: %s", scrapeErr.Code)
			t.Logf("   Message: %s", scrapeErr.Message)
			t.Logf("   Hints: %v", scrapeErr.Hints)
			t.Logf("   CanRetry: %v", scrapeErr.CanRetry)

			// Verify it's a proper ScrapeError
			if scrapeErr.Code == "" {
				t.Error("Error code should not be empty")
			}
			if len(scrapeErr.Hints) == 0 {
				t.Error("Error should provide hints for LLM")
			}

			// This is expected for wowhead (timeout or blocked)
			t.Logf("✅ Error handling works correctly - returning structured error")
		} else {
			t.Logf("❌ Scrape failed with error: %s", err.Error())
		}
	} else {
		t.Logf("✅ Scrape succeeded!")
		t.Logf("   Status: %d", result.StatusCode)
		t.Logf("   Size: %d bytes", len(result.HTML))
		t.Logf("   Duration: %v", result.Duration)

		if len(result.HTML) > 0 {
			t.Logf("   HTML preview (first 200 chars): %.200s", result.HTML)
		}
	}

	// Test diagnostic
	t.Log("\n--- Testing Diagnostic ---")

	// Create chrome scraper for diagnostic (нужен browser pool, но у нас его нет в тестах)
	// Поэтому пропустим полный diagnostic test
	t.Log("Note: Full diagnostic requires browser pool (not available in unit tests)")
}

// TestScrapeErrorStructure тест структуры ScrapeError
func TestScrapeErrorStructure(t *testing.T) {
	t.Log("Testing ScrapeError structure...")

	// Create sample error
	err := &ScrapeError{
		Code:     "timeout",
		Message:  "Request timeout after 30s",
		Hints:    []string{"try_screenshot", "diagnostic_url"},
		CanRetry: true,
	}

	// Test Error() method
	if err.Error() != err.Message {
		t.Errorf("Error() should return Message, got: %s", err.Error())
	}

	// Test fields
	if err.Code != "timeout" {
		t.Errorf("Code should be 'timeout', got: %s", err.Code)
	}

	if len(err.Hints) != 2 {
		t.Errorf("Should have 2 hints, got: %d", len(err.Hints))
	}

	if !err.CanRetry {
		t.Error("CanRetry should be true for timeout")
	}

	t.Log("✅ ScrapeError structure is correct")
}

// TestRetryLogic тест retry logic
func TestRetryLogic(t *testing.T) {
	t.Log("Testing retry logic...")

	// Create mock scraper that fails twice then succeeds
	attempts := 0
	mockScraper := &mockFailTwiceScraper{&attempts}

	retryScraper := NewRetryScraper(mockScraper, RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	})

	ctx := context.Background()
	result, err := retryScraper.Scrape(ctx, "https://example.com", Options{})

	if err != nil {
		t.Errorf("Should succeed after retries, got error: %v", err)
	}

	if result == nil {
		t.Error("Result should not be nil after successful retry")
	}

	if attempts != 3 {
		t.Errorf("Should have made 3 attempts, got: %d", attempts)
	}

	t.Logf("✅ Retry logic works correctly (succeeded after %d attempts)", attempts)
}

// Mock scrapers for testing

type mockFailTwiceScraper struct {
	attempts *int
}

func (m *mockFailTwiceScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
	*m.attempts++

	if *m.attempts < 3 {
		return nil, &ScrapeError{
			Code:     "timeout",
			Message:  "Timeout",
			Hints:    []string{"retry"},
			CanRetry: true,
		}
	}

	return &Result{
		HTML:       "<html>Success after retry</html>",
		StatusCode: 200,
		Method:     "mock",
	}, nil
}

func (m *mockFailTwiceScraper) Name() string {
	return "mock_fail_twice"
}

func (m *mockFailTwiceScraper) SupportsJS() bool {
	return false
}

func (m *mockFailTwiceScraper) SupportsActions() bool {
	return false
}

// TestNoRetryForBlockedErrors проверка что blocked errors не retry
func TestNoRetryForBlockedErrors(t *testing.T) {
	t.Log("Testing that blocked errors don't retry...")

	attempts := 0
	mockScraper := &mockBlockedScraper{&attempts}

	retryScraper := NewRetryScraper(mockScraper, RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	})

	ctx := context.Background()
	_, err := retryScraper.Scrape(ctx, "https://example.com", Options{})

	if err == nil {
		t.Error("Should fail for blocked error")
	}

	var scrapeErr *ScrapeError
	if errors.As(err, &scrapeErr) && scrapeErr != nil {
		if scrapeErr.Code != "blocked" {
			t.Errorf("Error code should be 'blocked', got: %s", scrapeErr.Code)
		}
	}

	if attempts != 1 {
		t.Errorf("Should have made only 1 attempt (no retry for blocked), got: %d", attempts)
	}

	t.Logf("✅ Correctly skipped retry for blocked error (only %d attempt)", attempts)
}

type mockBlockedScraper struct {
	attempts *int
}

func (m *mockBlockedScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
	*m.attempts++
	return nil, &ScrapeError{
		Code:     "blocked",
		Message:  "Blocked by Cloudflare",
		Hints:    []string{"try_screenshot"},
		CanRetry: false, // NO retry for blocked
	}
}

func (m *mockBlockedScraper) Name() string {
	return "mock_blocked"
}

func (m *mockBlockedScraper) SupportsJS() bool {
	return false
}

func (m *mockBlockedScraper) SupportsActions() bool {
	return false
}
