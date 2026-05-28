package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/converter"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

// ChromeScraper скрапер для динамических сайтов (JavaScript)
type ChromeScraper struct {
	cache       *cache.Cache
	browserPool *browser.Pool
	ragConfig   config.RAGConfig
	uaRotator   *useragent.Rotator
	proxy       *proxy.Rotator
	converter   *converter.Converter
	logger      zerolog.Logger
}

// NewChromeScraper создает новый ChromeScraper
func NewChromeScraper(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *ChromeScraper {
	return &ChromeScraper{
		cache:       cache,
		browserPool: browserPool,
		ragConfig:   ragConfig,
		uaRotator:   uaRotator,
		proxy:       proxy,
		converter:   converter.New(),
		logger:      logger.Get(),
	}
}

// Scrape реализует интерфейс Scraper
func (s *ChromeScraper) Scrape(ctx context.Context, urlStr string, opts Options) (*Result, error) {
	startTime := time.Now()

	// 0. Validate browser pool (CRITICAL for Chrome scraper)
	if s.browserPool == nil {
		return nil, fmt.Errorf("browser pool is not initialized - Chrome scraping is not available in this environment")
	}

	// 1. Validate URL
	if _, err := ValidateURL(urlStr); err != nil {
		return nil, err
	}

	// 2. Check cache (bypass if actions present)
	hasActions := len(opts.Actions) > 0
	if s.cache != nil && s.cache.IsEnabled() && !hasActions {
		cacheKey := GenerateCacheKeyJS(urlStr, OptsToMap(opts))
		if cached, found := s.cache.Get(ctx, cacheKey); found {
			s.logger.Info().
				Str("url", urlStr).
				Str("cache_key", cacheKey).
				Msg("Cache hit")

			result := &Result{
				HTML:        string(cached.Data),
				URL:         urlStr,
				FromCache:   true,
				Method:      s.Name(),
				ContentType: cached.Headers["content_type"],
				StatusCode:  200, // Cached responses are successful
				SizeBytes:   len(cached.Data),
			}

			// Add title if available
			if title, ok := cached.Headers["title"]; ok {
				result.Title = title
			}

			// Add format if available
			if format, ok := cached.Headers["format"]; ok {
				result.Format = format
			}

			// Add final_url if available
			if finalURL, ok := cached.Headers["final_url"]; ok {
				result.FinalURL = finalURL
			}

			// Add screenshot if available in cache
			if len(cached.Screenshot) > 0 {
				result.Screenshot = cached.Screenshot
			}

			return result, nil
		}
	}

	// 3. Get browser context
	browserCtx, browserCancel, err := s.browserPool.GetContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get browser context: %w", err)
	}
	defer browserCancel()

	// 4. Get User-Agent
	userAgent := opts.UserAgent
	if userAgent == "" && s.uaRotator != nil {
		userAgent = s.uaRotator.Get()
	}
	if userAgent == "" {
		userAgent = "MCP-Web-Scrape/1.0 (+https://github.com/metall/mcp-web-scrape)"
	}

	s.logger.Debug().
		Str("user_agent", userAgent).
		Str("url", urlStr).
		Msg("Using User-Agent for Chrome scraping")

	// 5. Setup stealth actions if enabled
	var stealth *browser.StealthActions
	if opts.StealthEnabled {
		stealth = browser.NewStealthActions(browser.StealthConfig{
			RandomDelay:    true,
			MinDelay:       100 * time.Millisecond,
			MaxDelay:       500 * time.Millisecond,
			EmulateScroll:  opts.StealthScroll,
			ScrollSteps:    3,
			MouseMovement:  opts.StealthMouse,
			RandomViewport: false,
		})
		s.logger.Info().
			Bool("stealth_enabled", true).
			Bool("stealth_scroll", opts.StealthScroll).
			Bool("stealth_mouse", opts.StealthMouse).
			Msg("Stealth mode enabled")
	}

	// 6. Get proxy if enabled
	var selectedProxy *proxy.Proxy
	if s.proxy != nil && s.proxy.IsEnabled() {
		selectedProxy, err = s.proxy.GetNext()
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get proxy, continuing without proxy")
		} else if selectedProxy != nil {
			s.logger.Info().
				Str("proxy", selectedProxy.URL).
				Msg("Using proxy for request")
		}
	}

	// 7. Build Chrome tasks
	tasks := s.buildChromeTasks(urlStr, userAgent, stealth, opts)

	// 8. Run tasks
	var html string
	var screenshotData []byte
	var title string
	var finalURL string

	chromeErr := chromedp.Run(browserCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Execute all tasks
			for _, task := range tasks {
				if err := task.Do(ctx); err != nil {
					return err
				}
			}

			// Get content
			if err := chromedp.OuterHTML("html", &html, chromedp.ByQuery).Do(ctx); err != nil {
				return err
			}

			// Get title
			if err := chromedp.Title(&title).Do(ctx); err != nil {
				return err
			}

			// Get final URL
			if err := chromedp.Location(&finalURL).Do(ctx); err != nil {
				return err
			}

			// Take screenshot if requested
			if opts.Screenshot || opts.ScreenshotMode == "auto" || opts.ScreenshotMode == "always" {
				if err := chromedp.FullScreenshot(&screenshotData, 90).Do(ctx); err != nil {
					s.logger.Warn().Err(err).Msg("Failed to take screenshot")
				}
			}

			return nil
		}),
	)

	if chromeErr != nil {
		// Chrome failed, try HTTP fallback
		s.logger.Warn().
			Str("url", urlStr).
			Err(chromeErr).
			Msg("Chrome scraping failed, attempting HTTP fallback")

		return s.httpFallback(ctx, urlStr, userAgent, startTime)
	}

	duration := time.Since(startTime)

	// 9. Optimize HTML
	originalHTMLSize := len(html)
	if strings.Contains(urlStr, "github.com") {
		html = string(OptimizeGitHubHTML([]byte(html)))
	} else {
		html = string(OptimizeHTML([]byte(html)))
	}

	optimizedSize := len(html)
	s.logger.Info().
		Int("original_size", originalHTMLSize).
		Int("optimized_size", optimizedSize).
		Int("reduction", originalHTMLSize-optimizedSize).
		Float64("reduction_percent", float64(originalHTMLSize-optimizedSize)/float64(originalHTMLSize)*100).
		Msg("HTML optimized for inference")

	// 10. Convert to Markdown if requested
	var converterStats *converter.ConversionStats
	if opts.OutputFormat == "markdown" {
		var convertedHTML string
		var err error

		convertedHTML, converterStats, err = s.converter.ConvertWithStats(html, converter.FormatMarkdown)
		if err != nil {
			s.logger.Warn().
				Err(err).
				Str("output_format", opts.OutputFormat).
				Msg("Markdown conversion failed, falling back to HTML")
		} else {
			html = convertedHTML
			s.logger.Info().
				Int("html_size", optimizedSize).
				Int("markdown_size", converterStats.FinalSize).
				Int("reduction", converterStats.Reduction).
				Float64("reduction_percent", converterStats.ReductionPct).
				Msg("Converted HTML to Markdown")
		}
	}

	// 11. Build actions metadata
	var actionsMetadata *ActionsMetadata
	if hasActions {
		actionTypes := make([]string, len(opts.Actions))
		for i, action := range opts.Actions {
			actionTypes[i] = action.Type
		}
		actionsMetadata = &ActionsMetadata{
			Count: len(opts.Actions),
			Types: actionTypes,
		}
	}

	// 12. Build result
	// Determine content type based on format
	contentType := "text/html"
	if opts.OutputFormat == "markdown" {
		contentType = "text/markdown"
	}

	result := &Result{
		HTML:            html,
		Title:           title,
		URL:             urlStr,
		FinalURL:        finalURL,
		StatusCode:      200,
		ContentType:     contentType,
		Duration:        duration,
		SizeBytes:       len(html),
		Screenshot:      screenshotData,
		Format:          opts.OutputFormat,
		FromCache:       false,
		ActionsMetadata: actionsMetadata,
		Method:          s.Name(),
	}

	// 13. Store in cache (only if no actions)
	if s.cache != nil && s.cache.IsEnabled() && !hasActions {
		cacheKey := GenerateCacheKeyJS(urlStr, OptsToMap(opts))
		ttl := s.cache.GetTTLForContentType("text/html")

		cachedResp := &cache.CachedResponse{
			Data:      []byte(html),
			Timestamp: time.Now(),
			Headers: map[string]string{
				"content_type": contentType,
				"title":        title,
				"final_url":    finalURL,
				"format":       opts.OutputFormat,
			},
		}

		// Store screenshot in cache if taken
		if len(screenshotData) > 0 {
			cachedResp.Screenshot = screenshotData
			cachedResp.Headers["screenshot_size"] = fmt.Sprintf("%d", len(screenshotData))
		}

		if err := s.cache.Set(ctx, cacheKey, cachedResp, ttl); err != nil {
			s.logger.Error().
				Str("cache_key", cacheKey).
				Err(err).
				Msg("Failed to store in cache")
		} else {
			s.logger.Info().
				Str("cache_key", cacheKey).
				Dur("ttl", ttl).
				Msg("Stored in cache")
		}
	}

	s.logger.Info().
		Str("url", urlStr).
		Str("final_url", finalURL).
		Int("size_bytes", len(html)).
		Str("format", opts.OutputFormat).
		Int64("duration_ms", duration.Milliseconds()).
		Msg("Successfully scraped URL with JavaScript")

	return result, nil
}

// Name возвращает название скрапера
func (s *ChromeScraper) Name() string {
	return "Chrome"
}

// SupportsJS возвращает true
func (s *ChromeScraper) SupportsJS() bool {
	return true
}

// SupportsActions возвращает true
func (s *ChromeScraper) SupportsActions() bool {
	return true
}

// buildChromeTasks строит список Chrome задач
func (s *ChromeScraper) buildChromeTasks(urlStr, userAgent string, stealth *browser.StealthActions, opts Options) []chromedp.Action {
	tasks := []chromedp.Action{
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Set User-Agent via CDP before navigation
			var result interface{}
			return chromedp.Evaluate(`Object.defineProperty(navigator, 'userAgent', {get: function() {return "`+userAgent+`"}})`, &result).Do(ctx)
		}),
	}

	// Navigate with stealth if enabled
	navigateAction := chromedp.Navigate(urlStr)
	if stealth != nil {
		navigateAction = stealth.ApplyStealth(navigateAction)
	}
	tasks = append(tasks, navigateAction)

	// Wait for specific selector if provided
	if opts.WaitForSelector != "" {
		waitAction := chromedp.WaitVisible(opts.WaitForSelector, chromedp.ByQuery)
		if stealth != nil {
			waitAction = stealth.ApplyStealth(waitAction)
		}
		tasks = append(tasks, waitAction)
	} else {
		// Wait for page load by default
		waitAction := chromedp.WaitReady("body", chromedp.ByQuery)
		if stealth != nil {
			waitAction = stealth.ApplyStealth(waitAction)
		}
		tasks = append(tasks, waitAction)
	}

	// Wait strategy: Network Idle vs Fixed time
	if opts.WaitForNetworkIdle {
		waitAction := browser.NetworkIdleAdvanced(browser.NetworkIdleOption{
			Timeout:    30 * time.Second,
			MinWait:    opts.WaitForDuration,
			CheckCount: 3,
		})
		tasks = append(tasks, waitAction)
		s.logger.Info().
			Bool("network_idle", true).
			Int("min_wait_ms", int(opts.WaitForDuration.Milliseconds())).
			Msg("Using network idle wait strategy")
	} else {
		tasks = append(tasks, chromedp.Sleep(opts.WaitForDuration))
		s.logger.Debug().
			Int("wait_ms", int(opts.WaitForDuration.Milliseconds())).
			Msg("Using fixed delay wait strategy")
	}

	// Add stealth scroll after page load if enabled
	if stealth != nil && opts.StealthScroll {
		tasks = append(tasks, stealth.EmulateScroll())
		s.logger.Debug().
			Msg("Added stealth scroll emulation")
	}

	// Execute interactive actions if provided
	if len(opts.Actions) > 0 {
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			s.logger.Info().
				Int("actions_count", len(opts.Actions)).
				Msg("Executing interactive actions")

			actionExecutor := browser.NewActionExecutor(s.logger, stealth)
			if err := actionExecutor.ExecuteActions(ctx, opts.Actions); err != nil {
				return fmt.Errorf("failed to execute actions: %w", err)
			}

			s.logger.Info().
				Msg("All interactive actions completed successfully")
			return nil
		}))
	}

	// Add stealth mouse movements before screenshot if enabled
	if stealth != nil && opts.StealthMouse {
		tasks = append(tasks, stealth.EmulateMouseMovement())
		s.logger.Debug().
			Msg("Added stealth mouse movement emulation")
	}

	return tasks
}

// httpFallback performs HTTP fallback when Chrome fails
func (s *ChromeScraper) httpFallback(ctx context.Context, urlStr, userAgent string, startTime time.Time) (*Result, error) {
	// Create HTTP client with redirect following
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// Add proxy to HTTP client if enabled
	if s.proxy != nil && s.proxy.IsEnabled() {
		client.Transport = &http.Transport{
			Proxy: s.proxy.GetProxyFunc(),
		}
		s.logger.Info().
			Msg("Using proxy for HTTP fallback")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set realistic browser headers
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := client.Do(req)
	if err != nil {
		if s.proxy != nil && s.proxy.IsEnabled() {
			s.proxy.MarkFailure(err)
		}
		return nil, fmt.Errorf("HTTP fallback also failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if s.proxy != nil && s.proxy.IsEnabled() {
			s.proxy.MarkFailure(fmt.Errorf("HTTP %d", resp.StatusCode))
		}
		return nil, fmt.Errorf("HTTP fallback failed with status %d", resp.StatusCode)
	}

	// Mark proxy as successful
	if s.proxy != nil && s.proxy.IsEnabled() {
		s.proxy.MarkSuccess()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response: %w", err)
	}

	// Check if response is too small
	if len(body) < 100 {
		s.logger.Warn().
			Int("size", len(body)).
			Msg("HTTP fallback returned very small response, might be an error page")
	}

	// Optimize HTML
	html := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		if strings.Contains(urlStr, "github.com") {
			html = string(OptimizeGitHubHTML(body))
		} else {
			html = string(OptimizeHTML(body))
		}
	}

	s.logger.Info().
		Str("method", "HTTP fallback").
		Int("size", len(html)).
		Str("final_url", resp.Request.URL.String()).
		Msg("Successfully scraped with HTTP fallback")

	return &Result{
		HTML:        html,
		URL:         urlStr,
		FinalURL:    resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Duration:    time.Since(startTime),
		SizeBytes:   len(html),
		Format:      "html",
		FromCache:   false,
		Method:      "HTTP Fallback",
	}, nil
}

