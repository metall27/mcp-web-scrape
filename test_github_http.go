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
		IsolatedMode:   true,
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

	// Test GitHub releases page
	testURL := "https://github.com/open-webui/open-webui/releases"

	fmt.Printf("🧪 Testing GitHub with HTTP Fallback\n")
	fmt.Printf("📍 URL: %s\n\n", testURL)

	// Test options
	opts := tools.Options{
		Timeout:        60 * time.Second,
		WaitForDuration: 3 * time.Second,
		OutputFormat:   "markdown",
		StealthEnabled: false,
	}

	ctx := context.Background()
	result, err := scraper.Scrape(ctx, testURL, opts)

	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
		os.Exit(1)
	}

	// Check result
	fmt.Printf("✅ SUCCESS\n")
	fmt.Printf("📊 Stats:\n")
	fmt.Printf("   - Size: %d bytes\n", result.SizeBytes)
	fmt.Printf("   - Duration: %d ms\n", result.Duration.Milliseconds())
	fmt.Printf("   - Format: %s\n", result.Format)
	fmt.Printf("   - Cached: %v\n", result.FromCache)
	fmt.Printf("   - Method: %s\n", result.Method)
	fmt.Printf("   - Status Code: %d\n", result.StatusCode)

	// Check if response is valid
	if result.SizeBytes < 500 {
		fmt.Printf("⚠️  WARNING: Response too small (%d bytes), might be an error page\n", result.SizeBytes)
		fmt.Printf("📄 Content preview: %s\n", truncate(result.HTML, 300))
	} else if containsErrorIndicators(result.HTML) {
		fmt.Printf("⚠️  WARNING: Response contains error indicators\n")
		fmt.Printf("📄 Content preview: %s\n", truncate(result.HTML, 300))
	} else {
		fmt.Printf("✅ Content looks valid\n")
		fmt.Printf("📄 Title: %s\n", result.Title)
		fmt.Printf("📄 Content preview (first 500 chars):\n%s\n", truncate(result.HTML, 500))
	}

	fmt.Printf("\n🎉 GitHub HTTP Fallback test complete!\n")
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
