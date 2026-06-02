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
	logCfg := config.LogConfig{
		Level: "debug",
		Pretty: true,
	}
	logger.Init(logCfg)

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

	browserPool, _ := browser.New(browserPoolCfg)
	defer browserPool.Close()

	cacheCfg := config.CacheConfig{
		Enabled:     false, // Disable cache to see full response
		TTL:         5 * time.Minute,
		CleanupInt:  10 * time.Minute,
	}
	cache, _ := cache.New(cacheCfg)

	uaCfg := useragent.Config{}
	uaRotator := useragent.New(uaCfg)

	ragCfg := config.RAGConfig{}
	proxyCfg := proxy.Config{}
	proxyRotator, _ := proxy.New(proxyCfg)

	scraper := tools.NewChromeScraper(cache, browserPool, ragCfg, browserCfg, uaRotator, proxyRotator, config.GitHubConfig{})

	// Test GitHub releases page
	testURL := "https://github.com/open-webui/open-webui/releases"

	fmt.Printf("🧪 Testing GitHub with HTTP Fallback (Full Output)\n")
	fmt.Printf("📍 URL: %s\n\n", testURL)

	opts := tools.Options{
		Timeout:         60 * time.Second,
		WaitForDuration: 3 * time.Second,
		OutputFormat:    "html", // Get HTML to see full content
		StealthEnabled:  false,
	}

	ctx := context.Background()
	result, err := scraper.Scrape(ctx, testURL, opts)

	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ SUCCESS\n")
	fmt.Printf("📊 Stats:\n")
	fmt.Printf("   - Size: %d bytes\n", result.SizeBytes)
	fmt.Printf("   - Duration: %d ms\n", result.Duration.Milliseconds())
	fmt.Printf("   - Format: %s\n", result.Format)
	fmt.Printf("   - Cached: %v\n", result.FromCache)
	fmt.Printf("   - Method: %s\n", result.Method)
	fmt.Printf("   - Status Code: %d\n\n", result.StatusCode)

	// Show full HTML content (first 2000 chars)
	fmt.Printf("📄 Full HTML Content (first 2000 chars):\n%s\n\n", result.HTML)

	// Check for specific GitHub elements
	if contains(result.HTML, "You signed in with another tab") {
		fmt.Printf("❌ GitHub Session Error Detected!\n")
	} else if contains(result.HTML, "releases") {
		fmt.Printf("✅ GitHub Releases Content Found!\n")
	} else {
		fmt.Printf("⚠️  Unknown content type\n")
	}

	fmt.Printf("\n🎉 Test complete!\n")
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
