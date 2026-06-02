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

	fmt.Printf("🧪 Гибкая оптимизация GitHub releases\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Test different optimization levels
	testCases := []struct {
		name string
		url  string
		desc string
	}{
		{
			name: "Баланс (по умолчанию)",
			url:  "https://github.com/open-webui/open-webui/releases",
			desc: "5 последних releases, оптимально для LLM",
		},
		{
			name: "Детальный режим",
			url:  "https://github.com/open-webui/open-webui/releases?releases=10",
			desc: "10 releases, более полная информация",
		},
		{
			name: "Эксперт режим",
			url:  "https://github.com/open-webui/open-webui/releases?releases=20",
			desc: "20 releases, много токенов но полная картина",
		},
	}

	ctx := context.Background()

	for i, tc := range testCases {
		fmt.Printf("Test %d: %s\n", i+1, tc.name)
		fmt.Printf("URL: %s\n", tc.url)
		fmt.Printf("Описание: %s\n", tc.desc)

		opts := tools.Options{
			Timeout:         60 * time.Second,
			WaitForDuration: 3 * time.Second,
			OutputFormat:    "markdown",
			StealthEnabled:  false,
		}

		startTime := time.Now()
		result, err := scraper.Scrape(ctx, tc.url, opts)
		duration := time.Since(startTime)

		if err != nil {
			fmt.Printf("❌ FAILED: %v\n\n", err)
			continue
		}

		// Calculate tokens
		tokens := result.SizeBytes / 4
		cost := float64(tokens) * 0.00003 / 1000

		fmt.Printf("✅ SUCCESS\n")
		fmt.Printf("📊 Результаты:\n")
		fmt.Printf("   Размер: %d bytes (%s)\n", result.SizeBytes, result.Format)
		fmt.Printf("   Токены: ~%d tokens\n", tokens)
		fmt.Printf("   Стоимость: $%.4f\n", cost)
		fmt.Printf("   Длительность: %d ms\n", duration.Milliseconds())

		// Rating
		fmt.Printf("   Рейтинг: ")
		if tokens < 1000 {
			fmt.Printf("✅ Отлично для LLM\n")
		} else if tokens < 5000 {
			fmt.Printf("⚠️  Приемлемо для LLM\n")
		} else if tokens < 15000 {
			fmt.Printf("❌ Много для LLM\n")
		} else {
			fmt.Printf("❌ Слишком много для LLM!\n")
		}

		// Show content preview
		fmt.Printf("📄 Превью (первые 200 символов):\n")
		preview := result.HTML
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		fmt.Printf("%s\n\n", preview)

		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("💡 Рекомендации по использованию:\n")
	fmt.Printf("   • По умолчанию (5 releases) для обычных запросов\n")
	fmt.Printf("   • ?releases=10 для исследований версий\n")
	fmt.Printf("   • ?releases=20 для глубокого анализа\n")
	fmt.Printf("   • ?releases=all только при необходимости!\n")
	fmt.Printf("\n🎉 Тестирование завершено!\n")
}
