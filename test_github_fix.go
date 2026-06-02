package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/metall/mcp-web-scrape/internal/mcp/tools"
	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
)

func main() {
	// Initialize logger
	logCfg := config.LogConfig{
		Level: "debug",
		Pretty: true,
	}
	if err := logger.Init(logCfg); err != nil {
		fmt.Printf("❌ Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	// Initialize components
	browserCfg := config.BrowserConfig{
		MaxRetries:     1,
		BlockDetection: true,
		PollingConfig: config.PollingConfig{
			MaxAttempts: 60,
			Interval:    100 * time.Millisecond,
		},
	}

	browserPoolCfg := browser.Config{
		Logger:         logger.Get(),
		MaxTabs:        5,
		Headless:       true,
		DisableGPU:     true,
		NoSandbox:      true,
		ViewportWidth:  1920,
		ViewportHeight: 1080,
		IsolatedMode:   true, // Enable isolated mode to fix GitHub session conflicts
	}

	browserPool, err := browser.New(browserPoolCfg)
	if err != nil {
		fmt.Printf("❌ Failed to create browser pool: %v\n", err)
		os.Exit(1)
	}
	defer browserPool.Close()

	// Initialize cache
	cacheCfg := config.CacheConfig{
		Enabled:     true,
		TTL:         5 * time.Minute,
		CleanupInt:  10 * time.Minute,
	}
	cache, err := cache.New(cacheCfg)
	if err != nil {
		fmt.Printf("❌ Failed to initialize cache: %v\n", err)
		os.Exit(1)
	}

	// Initialize UA rotator
	uaCfg := useragent.Config{}
	uaRotator := useragent.New(uaCfg)

	// Initialize Chrome scraper
	ragCfg := config.RAGConfig{}
	proxyCfg := proxy.Config{}
	proxyRotator, err := proxy.New(proxyCfg)
	if err != nil {
		fmt.Printf("❌ Failed to initialize proxy rotator: %v\n", err)
		os.Exit(1)
	}

	scraper := tools.NewChromeScraper(cache, browserPool, ragCfg, browserCfg, uaRotator, proxyRotator, config.GitHubConfig{})

	// Test URL
	testURL := "https://github.com/open-webui/open-webui/releases"

	fmt.Printf("🧪 Testing GitHub scraping with fixes...\n")
	fmt.Printf("📍 URL: %s\n", testURL)
	fmt.Printf("🔧 Mode: Isolated browser instances\n\n", testURL)

	// Test with different options
	testCases := []struct {
		name string
		opts tools.Options
	}{
		{
			name: "ADVANCED Stealth + Canvas Protection",
			opts: tools.Options{
				Timeout:            60 * time.Second,
				WaitForDuration:    5 * time.Second,
				OutputFormat:       "markdown",
				StealthEnabled:     true,
				StealthScroll:      true,
				StealthMouse:       true,
			},
		},
		{
			name: "NO Stealth - Clean mode",
			opts: tools.Options{
				Timeout:            60 * time.Second,
				WaitForDuration:    5 * time.Second,
				OutputFormat:       "markdown",
				StealthEnabled:     false,
				StealthScroll:      false,
			},
		},
		{
			name: "Basic scraping",
			opts: tools.Options{
				Timeout:            60 * time.Second,
				WaitForDuration:    3 * time.Second,
				OutputFormat:       "markdown",
				StealthEnabled:     false,
			},
		},
		{
			name: "Network idle + Stealth",
			opts: tools.Options{
				Timeout:            60 * time.Second,
				WaitForNetworkIdle: true,
				WaitForDuration:    5 * time.Second,
				OutputFormat:       "markdown",
				StealthEnabled:     true,
				StealthScroll:      true,
			},
		},
	}

	for i, tc := range testCases {
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Test %d: %s\n", i+1, tc.name)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

		ctx := context.Background()
		result, err := scraper.Scrape(ctx, testURL, tc.opts)

		if err != nil {
			fmt.Printf("❌ FAILED: %v\n\n", err)
			continue
		}

		// Check result
		fmt.Printf("✅ SUCCESS\n")
		fmt.Printf("📊 Stats:\n")
		fmt.Printf("   - Size: %d bytes\n", result.SizeBytes)
		fmt.Printf("   - Duration: %d ms\n", result.Duration.Milliseconds())
		fmt.Printf("   - Format: %s\n", result.Format)
		fmt.Printf("   - Cached: %v\n", result.FromCache)

		// Check if response is valid (not an error page)
		if result.SizeBytes < 500 {
			fmt.Printf("⚠️  WARNING: Response too small (%d bytes), might be an error page\n", result.SizeBytes)
			fmt.Printf("📄 Content preview: %s\n", truncate(result.HTML, 200))
		} else if containsErrorIndicators(result.HTML) {
			fmt.Printf("⚠️  WARNING: Response contains error indicators\n")
			fmt.Printf("📄 Content preview: %s\n", truncate(result.HTML, 200))
		} else {
			fmt.Printf("✅ Content looks valid\n")
			fmt.Printf("📄 Title: %s\n", result.Title)
		}

		fmt.Printf("\n")

		// Add delay between tests
		if i < len(testCases)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("🎉 Testing complete!\n")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func containsErrorIndicators(html string) bool {
	errorIndicators := []string{
		"You signed in with another tab",
		"signed out in another tab",
		"Reload to refresh your session",
		"{{ message }}",
	}

	for _, indicator := range errorIndicators {
		if contains(html, indicator) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
