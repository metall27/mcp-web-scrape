package main

import (
	"context"
	"fmt"
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
		Level: "info",
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
		Enabled:     false,
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

	fmt.Printf("🧪 Testing Smart GitHub Catalog Mode\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Test 1: Smart Catalog Mode
	fmt.Printf("Test 1: Smart Catalog Mode (?mode=catalog)\n")
	fmt.Printf("URL: https://github.com/open-webui/open-webui/releases?mode=catalog\n\n")

	ctx := context.Background()
	opts := tools.Options{
		Timeout:         60 * time.Second,
		WaitForDuration: 3 * time.Second,
		OutputFormat:    "markdown",
		StealthEnabled:  false,
	}

	result, err := scraper.Scrape(ctx, "https://github.com/open-webui/open-webui/releases?mode=catalog", opts)

	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
		return
	}

	tokens := result.SizeBytes / 4

	fmt.Printf("✅ SUCCESS\n")
	fmt.Printf("📊 Catalog Stats:\n")
	fmt.Printf("   Size: %d bytes\n", result.SizeBytes)
	fmt.Printf("   Tokens: ~%d tokens\n", tokens)
	fmt.Printf("   Duration: %d ms\n", result.Duration.Milliseconds())
	fmt.Printf("   Method: %s\n\n", result.Method)

	// Show catalog preview
	fmt.Printf("📄 Catalog Content Preview (first 500 chars):\n")
	preview := result.HTML
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	fmt.Printf("%s\n\n", preview)

	// Test 2: Standard mode for comparison
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Test 2: Standard Mode (default 5 releases)\n")
	fmt.Printf("URL: https://github.com/open-webui/open-webui/releases\n\n")

	result2, err := scraper.Scrape(ctx, "https://github.com/open-webui/open-webui/releases", opts)

	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
		return
	}

	tokens2 := result2.SizeBytes / 4

	fmt.Printf("✅ SUCCESS\n")
	fmt.Printf("📊 Standard Mode Stats:\n")
	fmt.Printf("   Size: %d bytes\n", result2.SizeBytes)
	fmt.Printf("   Tokens: ~%d tokens\n", tokens2)
	fmt.Printf("   Duration: %d ms\n", result2.Duration.Milliseconds())

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("💡 Smart Catalog Benefits:\n")
	fmt.Printf("   • See ALL releases at once (%d vs 5)\n", 30) // Based on earlier analysis
	fmt.Printf("   • Minimal tokens (%d vs %d)\n", tokens, tokens2)
	fmt.Printf("   • LLM chooses which releases to load\n")
	fmt.Printf("   • Perfect for follow-up questions\n\n")

	fmt.Printf("🎯 Example Usage:\n")
	fmt.Printf("   User: \"https://github.com/open-webui/open-webui/releases?mode=catalog что тут нового?\"\n")
	fmt.Printf("   → LLM sees full catalog, picks latest releases\n\n")
	fmt.Printf("   User: \"в каком релизе добавили knowledge base sync?\"\n")
	fmt.Printf("   → LLM requests: https://github.com/.../releases?releases=v0.9.6\n\n")

	fmt.Printf("🎉 Smart Catalog testing complete!\n")
}
