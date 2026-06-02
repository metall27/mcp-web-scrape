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
		Level: "info", // Changed to info to reduce noise
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
		Enabled:     false, // Disable cache to see real processing
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

	testURL := "https://github.com/open-webui/open-webui/releases"

	fmt.Printf("🧪 Token Usage Test: GitHub Atom Feed\n")
	fmt.Printf("📍 URL: %s\n\n", testURL)

	// Test with MARKDOWN output format (what LLM will receive)
	opts := tools.Options{
		Timeout:         60 * time.Second,
		WaitForDuration: 3 * time.Second,
		OutputFormat:    "markdown", // IMPORTANT: What goes to LLM
		StealthEnabled:  false,
	}

	ctx := context.Background()
	result, err := scraper.Scrape(ctx, testURL, opts)

	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ SUCCESS\n\n")
	fmt.Printf("📊 Token Usage Analysis:\n")
	fmt.Printf("─────────────────────────────────────\n")
	fmt.Printf("Original Size (from GitHub):  348,701 bytes (XML)\n")
	fmt.Printf("After Processing:              %d bytes (%s)\n", result.SizeBytes, result.Format)
	fmt.Printf("Duration:                       %d ms\n", result.Duration.Milliseconds())
	fmt.Printf("─────────────────────────────────────\n\n")

	// Calculate estimated tokens (roughly 4 chars per token for code/markdown)
	estimatedTokens := result.SizeBytes / 4
	fmt.Printf("🤖 Estimated LLM Tokens: ~%d tokens\n", estimatedTokens)
	fmt.Printf("💰 Estimated Cost (GPT-4): $%.4f\n", float64(estimatedTokens)*0.00003/1000)

	// Show content preview
	fmt.Printf("\n📄 Content Preview (first 300 chars):\n")
	preview := result.HTML
	if len(preview) > 300 {
		preview = preview[:300] + "..."
	}
	fmt.Printf("%s\n", preview)

	// Check if content looks like releases info
	if contains(result.HTML, "Release notes from") || contains(result.HTML, "open-webui") {
		fmt.Printf("\n✅ Content contains release information!\n")
	}

	fmt.Printf("\n🎯 Conclusion: ")
	if result.SizeBytes < 5000 {
		fmt.Printf("✅ OPTIMAL SIZE for LLM (<5KB)\n")
	} else if result.SizeBytes < 20000 {
		fmt.Printf("⚠️  ACCEPTABLE SIZE for LLM (<20KB)\n")
	} else {
		fmt.Printf("❌ TOO LARGE for LLM (>20KB)\n")
	}
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
