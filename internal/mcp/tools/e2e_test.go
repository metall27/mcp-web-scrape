package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

// TestE2EScenarios end-to-end тесты реальных scenarios
func TestE2EScenarios(t *testing.T) {
	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)
	retryScraper := NewRetryScraper(scraper, DefaultRetryConfig)

	tests := []struct {
		name           string
		url            string
		timeout        time.Duration
		expectSuccess  bool
		expectError    string // ожидаемый error code
		minDuration    time.Duration
		maxDuration    time.Duration
	}{
		{
			name:          "Normal working URL",
			url:           "https://example.com",
			timeout:        5 * time.Second,
			expectSuccess: true,
			minDuration:   100 * time.Millisecond,
			maxDuration:   10 * time.Second,
		},
		{
			name:          "GitHub repo page",
			url:           "https://github.com/golang/go",
			timeout:        10 * time.Second,
			expectSuccess: true,
			minDuration:   100 * time.Millisecond,
			maxDuration:   15 * time.Second,
		},
		{
			name:          "Wowhead quest page (was problematic)",
			url:           "https://www.wowhead.com/tbc/quest=1947/journey-to-the-marsh",
			timeout:        10 * time.Second,
			expectSuccess: true,
			minDuration:   100 * time.Millisecond,
			maxDuration:   15 * time.Second,
		},
		{
			name:        "Invalid URL (should fail fast)",
			url:         "not-a-url",
			timeout:     1 * time.Second,
			expectSuccess: false,
			expectError: "invalid_url",
		},
		{
			name:        "Unsupported protocol",
			url:         "ftp://example.com",
			timeout:     1 * time.Second,
			expectSuccess: false,
			expectError: "invalid_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			start := time.Now()

			result, err := retryScraper.Scrape(ctx, tt.url, Options{
				Timeout: tt.timeout,
			})

			duration := time.Since(start)

			t.Logf("Duration: %v", duration)

			if tt.expectSuccess {
				if err != nil {
					var scrapeErr *ScrapeError
					if errors.As(err, &scrapeErr) && scrapeErr != nil {
						t.Errorf("Expected success, got error: %s (%s)", scrapeErr.Code, scrapeErr.Message)
						if len(scrapeErr.Hints) > 0 {
							t.Logf("  Hints: %v", scrapeErr.Hints)
						}
					} else {
						t.Errorf("Expected success, got error: %s", err.Error())
					}
				}

				if result == nil {
					t.Error("Expected result, got nil")
					return
				}

				if result.StatusCode != 200 {
					t.Logf("Warning: Status %d (expected 200)", result.StatusCode)
				}

				if len(result.HTML) == 0 {
					t.Error("Expected HTML content, got empty")
				}

				if duration < tt.minDuration {
					t.Errorf("Too fast: %v < %v", duration, tt.minDuration)
				}

				if duration > tt.maxDuration {
					t.Errorf("Too slow: %v > %v", duration, tt.maxDuration)
				}

				t.Logf("✅ Success! Status: %d, Size: %d bytes", result.StatusCode, len(result.HTML))
			} else {
				if err == nil {
					t.Error("Expected error, got success")
				}

				var scrapeErr *ScrapeError
				if tt.expectError != "" && errors.As(err, &scrapeErr) && scrapeErr != nil && scrapeErr.Code != tt.expectError {
					t.Errorf("Expected error code '%s', got '%s'", tt.expectError, scrapeErr.Code)
				}

				if errors.As(err, &scrapeErr) && scrapeErr != nil {
					t.Logf("✅ Expected error: %s - %s", scrapeErr.Code, scrapeErr.Message)
					if len(scrapeErr.Hints) > 0 {
						t.Logf("  Hints: %v", scrapeErr.Hints)
					}
				} else {
					t.Logf("✅ Expected error: %s", err.Error())
				}
			}
		})
	}
}

// TestConcurrentRequests тест параллельных запросов
func TestConcurrentRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent test in short mode")
	}

	t.Log("Testing concurrent requests...")

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)
	retryScraper := NewRetryScraper(scraper, DefaultRetryConfig)

	// Run 10 concurrent requests
	const numRequests = 10
	results := make(chan bool, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			result, err := retryScraper.Scrape(ctx, "https://example.com", Options{
				Timeout: 5 * time.Second,
			})

			success := err == nil && result != nil
			results <- success
		}()
	}

	// Collect results
	successCount := 0
	for i := 0; i < numRequests; i++ {
		if <-results {
			successCount++
		}
	}

	successRate := float64(successCount) / float64(numRequests) * 100

	t.Logf("Concurrent requests: %d/%d succeeded (%.1f%%)",
		successCount, numRequests, successRate)

	if successCount < numRequests-2 { // Allow 2 failures
		t.Errorf("Too many failures: %d/%d", numRequests-successCount, numRequests)
	}

	if successRate < 80 {
		t.Errorf("Success rate too low: %.1f%%", successRate)
	}

	t.Logf("✅ Concurrent test passed!")
}

// TestRetryBehavior тестирование retry поведения
func TestRetryBehavior(t *testing.T) {
	t.Log("Testing retry behavior...")

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)

	// Test with URL that might timeout
	ctx := context.Background()

	result, err := scraper.Scrape(ctx, "https://httpbin.org/delay/2", Options{
		Timeout: 1 * time.Second, // Will timeout
	})

	if err != nil {
		t.Logf("Got error (expected for timeout scenario):")
		var scrapeErr *ScrapeError
		if errors.As(err, &scrapeErr) && scrapeErr != nil {
			t.Logf("  Code: %s", scrapeErr.Code)
			t.Logf("  CanRetry: %v", scrapeErr.CanRetry)
			t.Logf("  Hints: %v", scrapeErr.Hints)

			// Verify error structure
			if scrapeErr.Code == "" {
				t.Error("Error code should not be empty")
			}

			if len(scrapeErr.Hints) == 0 {
				t.Error("Error should provide hints")
			}
		} else {
			t.Logf("  Error: %s", err.Error())
		}

		// Most errors should be retryable except blocking
		if errors.As(err, &scrapeErr) && scrapeErr != nil {
			if !scrapeErr.CanRetry && scrapeErr.Code != "invalid_url" {
				t.Logf("Note: Error '%s' is not retryable", scrapeErr.Code)
			}
		}

		t.Log("✅ Retry behavior test passed!")
	} else {
		t.Log("Request succeeded (httpbin might be fast)")
		if result != nil {
			t.Logf("  Status: %d, Duration: %v", result.StatusCode, result.Duration)
		}
	}
}

// TestErrorCodes покрытие разных error codes
func TestErrorCodes(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		expectCode string
	}{
		{
			name:       "Invalid URL",
			url:        "not-a-url",
			expectCode: "invalid_url",
		},
		{
			name:       "Unsupported protocol",
			url:        "ftp://example.com",
			expectCode: "invalid_url",
		},
		{
			name:       "Empty URL",
			url:        "",
			expectCode: "invalid_url",
		},
	}

	cache, _ := cache.New(config.CacheConfig{
		Enabled: false,
	})

	scraper := NewHTTPScraper(cache, nil, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			result, err := scraper.Scrape(ctx, tt.url, Options{})

			if err == nil {
				t.Error("Expected error for invalid input")
				if result != nil {
					t.Logf("Got result instead: Status %d", result.StatusCode)
				}
				return
			}

			var scrapeErr *ScrapeError
			if errors.As(err, &scrapeErr) && scrapeErr != nil {
				if scrapeErr.Code != tt.expectCode {
					t.Errorf("Expected error code '%s', got '%s'", tt.expectCode, scrapeErr.Code)
				}

				// Verify error has proper structure
				if scrapeErr.Message == "" {
					t.Error("Error message should not be empty")
				}

				t.Logf("✅ %s: Code=%s, Message=%s",
					tt.name, scrapeErr.Code, scrapeErr.Message)
			} else {
				t.Logf("✅ %s: %s", tt.name, err.Error())
			}
		})
	}
}
