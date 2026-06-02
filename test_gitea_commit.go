package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	mcp "github.com/metall/mcp-web-scrape/internal/mcp"
	"github.com/metall/mcp-web-scrape/internal/mcp/tools"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logger, err := logger.New()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	cfg := &config.Config{
		Cache: config.CacheConfig{
			Enabled: false,
		},
		GitHub: config.GitHubConfig{
			Token: os.Getenv("GITHUB_TOKEN"),
		},
	}

	// Create scraper
	scraper, err := tools.NewChromeScraper(cfg, logger, nil, nil, nil, nil, nil, nil)
	if err != nil {
		logger.Fatal("Failed to create scraper", zap.Error(err))
	}
	defer scraper.Close()

	// Test URL
	testURL := "https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp"

	fmt.Printf("🧪 Testing Gitea repository scrape\n")
	fmt.Printf("URL: %s\n\n", testURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := &tools.ChromeOptions{
		URL:            testURL,
		OutputFormat:   "markdown",
		Timeout:        30,
		WaitTimeMs:     3000,
		StealthEnabled: false,
	}

	result, err := scraper.ScrapeWithJS(ctx, opts)
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ SUCCESS\n")
	fmt.Printf("Method: %s\n", result.Method)
	fmt.Printf("Size: %d bytes\n", result.SizeBytes)
	fmt.Printf("Duration: %v\n", result.Duration)

	// Show first 1500 chars to see commit info
	preview := result.HTML
	if len(preview) > 1500 {
		preview = preview[:1500] + "..."
	}

	fmt.Printf("\n📋 Content preview:\n%s\n", preview)
}