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
	"github.com/rs/zerolog/log"
)

func main() {
	// Load configuration
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := logger.Init(cfg.Log); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	log.Info().Msg("🧪 Phase 3 Extended Stealth - Direct Test")

	// Initialize browser pool
	browserPool, err := browser.New(browser.Config{
		Logger:         log.Logger,
		MaxTabs:        cfg.Browser.MaxTabs,
		Headless:       cfg.Browser.Headless,
		DisableGPU:     cfg.Browser.DisableGPU,
		NoSandbox:      cfg.Browser.NoSandbox,
		ViewportWidth:  cfg.Browser.ViewportWidth,
		ViewportHeight: cfg.Browser.ViewportHeight,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize browser pool")
	}
	defer browserPool.Close()

	log.Info().Msg("✅ Browser pool initialized")

	// Initialize components
	cacheInstance, err := cache.New(cfg.Cache)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize cache")
	}

	uaRotator := useragent.New(useragent.Config{
		CustomUserAgents: cfg.UserAgent.CustomUserAgents,
	})

	proxyRotator, err := proxy.New(proxy.Config{
		Proxies:       cfg.Proxy.Proxies,
		Enabled:       cfg.Proxy.Enabled,
		TestOnStartup: cfg.Proxy.TestOnStartup,
		TestTimeout:   time.Duration(cfg.Proxy.TestTimeout) * time.Second,
		Logger:        log.Logger,
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to initialize proxy rotator, continuing without proxy")
		proxyRotator, _ = proxy.New(proxy.Config{
			Proxies: []string{},
			Enabled: false,
			Logger:  log.Logger,
		})
	}

	// Create scraper
	scraper := tools.NewChromeScraper(cacheInstance, browserPool, cfg.RAG, cfg.Browser, uaRotator, proxyRotator, config.GitHubConfig{})

	// Test sites
	testSites := []struct {
		name string
		url  string
	}{
		{"pixelscan.net (fingerprint test)", "https://pixelscan.net"},
		{"bot.sannysoft.com (stealth score)", "https://bot.sannysoft.com"},
		{"arh.antoinevastel.com (headless detection)", "https://arh.antoinevastel.com/bots/areyouheadless"},
	}

	for i, site := range testSites {
		log.Info().Msg(fmt.Sprintf("🔍 Test %d: %s", i+1, site.name))
		log.Info().Str("url", site.url).Msg("Starting scrape")

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)

		opts := tools.Options{
			OutputFormat:    "html",
			StealthEnabled: true,
			StealthScroll:   true,
			StealthMouse:    false,
			WaitForDuration: 3 * time.Second,
		}

		startTime := time.Now()
		result, err := scraper.Scrape(ctx, site.url, opts)
		duration := time.Since(startTime)

		cancel()

		if err != nil {
			log.Error().Err(err).Str("site", site.name).Msg("❌ Scraping failed")
			continue
		}

		log.Info().
			Str("site", site.name).
			Str("title", result.Title).
			Int("size_bytes", result.SizeBytes).
			Str("method", result.Method).
			Bool("from_cache", result.FromCache).
			Dur("duration", duration).
			Msg("✅ Scraping successful")

		// Small delay between requests
		if i < len(testSites)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	log.Info().Msg("====================================")
	log.Info().Msg("📊 Phase 3 Testing Complete!")
	log.Info().Msg("")
	log.Info().Msg("💡 Manual verification needed:")
	log.Info().Msg("1. Open one of the tested sites in your browser")
	log.Info().Msg("2. Check console for:")
	log.Info().Msg("   - navigator.webdriver should be undefined")
	log.Info().Msg("   - navigator.plugins should have 3 plugins")
	log.Info().Msg("   - WebGL vendor/renderer should be realistic")
	log.Info().Msg("3. Verify timezone/locale are randomized per session")
}
