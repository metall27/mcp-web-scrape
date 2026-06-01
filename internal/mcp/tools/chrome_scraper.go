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
	tlshttp "github.com/metall/mcp-web-scrape/internal/pkg/http"
	"github.com/metall/mcp-web-scrape/internal/pkg/converter"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

// ChromeScraper скрапер для динамических сайтов (JavaScript)
type ChromeScraper struct {
	cache        *cache.Cache
	browserPool  *browser.Pool
	ragConfig    config.RAGConfig
	browserCfg   config.BrowserConfig
	uaRotator    *useragent.Rotator
	proxy        *proxy.Rotator
	converter    *converter.Converter
	logger       zerolog.Logger
}

// scrapeContext holds the context for a single scrape attempt (Phase 5: Retry Loop)
type scrapeContext struct {
	browserCtx    context.Context
	browserCancel context.CancelFunc
	proxy         *proxy.Proxy
	userAgent     string
	stealth       *browser.StealthActions
}

// NewChromeScraper создает новый ChromeScraper
func NewChromeScraper(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, browserCfg config.BrowserConfig, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *ChromeScraper {
	return &ChromeScraper{
		cache:       cache,
		browserPool: browserPool,
		ragConfig:   ragConfig,
		browserCfg:  browserCfg,
		uaRotator:   uaRotator,
		proxy:       proxy,
		converter:   converter.New(),
		logger:      logger.Get(),
	}
}

// createScrapeContext creates a new scrape context for a single attempt (Phase 5: Retry Loop)
// This method extracts browser context creation, proxy selection, and UA generation
func (s *ChromeScraper) createScrapeContext(ctx context.Context, urlStr string, opts Options) (*scrapeContext, error) {
	scrapeCtx := &scrapeContext{}

	// 1. Get browser context
	browserCtx, browserCancel, err := s.browserPool.GetContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get browser context: %w", err)
	}
	scrapeCtx.browserCtx = browserCtx
	scrapeCtx.browserCancel = browserCancel

	// 2. Get User-Agent
	userAgent := opts.UserAgent
	if userAgent == "" && s.uaRotator != nil {
		userAgent = s.uaRotator.Get()
	}
	if userAgent == "" {
		userAgent = "MCP-Web-Scrape/1.0 (+https://github.com/metall/mcp-web-scrape)"
	}
	scrapeCtx.userAgent = userAgent

	s.logger.Debug().
		Str("user_agent", userAgent).
		Str("url", urlStr).
		Msg("Using User-Agent for Chrome scraping")

	// 3. Setup stealth actions if enabled
	if opts.StealthEnabled {
		scrapeCtx.stealth = browser.NewStealthActions(browser.StealthConfig{
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

	// 4. Get proxy if enabled
	if s.proxy != nil && s.proxy.IsEnabled() {
		selectedProxy, err := s.proxy.GetNext()
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get proxy, continuing without proxy")
		} else if selectedProxy != nil {
			scrapeCtx.proxy = selectedProxy
			s.logger.Info().
				Str("proxy", selectedProxy.URL).
				Msg("Using proxy for scrape attempt")
		}
	}

	return scrapeCtx, nil
}

// scrapeAttemptResult holds the result of a single scrape attempt (Phase 5: Retry Loop)
type scrapeAttemptResult struct {
	html           string
	screenshotData []byte
	title          string
	finalURL       string
	isBlocked      bool
	blockResult    browser.BlockResult
	err            error
}

// scrapeAttempt performs a single scrape attempt (Phase 5: Retry Loop)
// This method executes Chrome tasks and returns scrape result or blocking detection
func (s *ChromeScraper) scrapeAttempt(ctx context.Context, urlStr string, scrapeCtx *scrapeContext, opts Options) scrapeAttemptResult {
	result := scrapeAttemptResult{}

	// 1. Build Chrome tasks
	tasks := s.buildChromeTasks(urlStr, scrapeCtx.userAgent, scrapeCtx.stealth, opts)

	// 2. Run tasks
	var html string
	var screenshotData []byte
	var title string
	var finalURL string

	chromeErr := chromedp.Run(scrapeCtx.browserCtx,
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
		// Chrome failed
		s.logger.Warn().
			Str("url", urlStr).
			Err(chromeErr).
			Msg("Chrome scraping failed for this attempt")

		result.err = chromeErr
		return result
	}

	// 3. Detect blocking if enabled
	if s.browserCfg.BlockDetection {
		blockResult, err := browser.DetectBlocking(ctx)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to detect blocking (non-critical)")
		} else if blockResult.IsBlocked {
			s.logger.Warn().
				Str("block_type", string(blockResult.BlockType)).
				Str("details", blockResult.Details).
				Float64("confidence", blockResult.Confidence).
				Msg("Block detected in this attempt")

			result.isBlocked = true
			result.blockResult = blockResult
			return result
		}
	}

	// 4. Success - return scraped data
	result.html = html
	result.screenshotData = screenshotData
	result.title = title
	result.finalURL = finalURL
	result.isBlocked = false
	result.err = nil

	return result
}


// Scrape реализует интерфейс Scraper
func (s *ChromeScraper) Scrape(ctx context.Context, urlStr string, opts Options) (*Result, error) {
	startTime := time.Now()

	// Apply tool-level timeout for scraping operations
	toolTimeout := s.browserCfg.ToolTimeout
	if toolTimeout == 0 {
		toolTimeout = 120 * time.Second // default timeout
	}

	toolCtx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

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

	// 3. Phase 5: Full Retry Loop Implementation
	// Attempt 1 → Block Detected → New Proxy → Retry 2 → Block Detected → New Proxy → Retry 3

	// Get max retries from config (default: 2)
	maxRetries := s.browserCfg.MaxRetries
	if maxRetries == 0 {
		maxRetries = 2 // default
	}

	// Track attempts
	var lastError error
	var successfulAttempt bool
	var html string
	var screenshotData []byte
	var title string
	var finalURL string

	// Retry loop
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			s.logger.Info().
				Int("attempt", attempt).
				Int("max_retries", maxRetries).
				Msg("Phase 5: Retrying with new proxy")
		}

		// Create scrape context for this attempt
		scrapeCtx, err := s.createScrapeContext(toolCtx, urlStr, opts)
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to create scrape context")
			lastError = err
			continue
		}

		// Ensure browser context is cleaned up after this attempt
		defer scrapeCtx.browserCancel()

		// Perform scrape attempt
		attemptResult := s.scrapeAttempt(toolCtx, urlStr, scrapeCtx, opts)

		// Handle result
		if attemptResult.err != nil {
			// Chrome execution error
			s.logger.Warn().
				Int("attempt", attempt).
				Err(attemptResult.err).
				Msg("Scrape attempt failed with error")

			// Mark proxy as failed if applicable
			if s.proxy != nil && s.proxy.IsEnabled() && scrapeCtx.proxy != nil {
				s.proxy.MarkFailure(attemptResult.err)
			}

			lastError = attemptResult.err
			continue
		}

		if attemptResult.isBlocked {
			// Blocking detected
			s.logger.Warn().
				Int("attempt", attempt).
				Str("block_type", string(attemptResult.blockResult.BlockType)).
				Float64("confidence", attemptResult.blockResult.Confidence).
				Msg("Phase 5: Blocking detected, will retry with different proxy")

			// Mark current proxy as failed
			if s.proxy != nil && s.proxy.IsEnabled() && scrapeCtx.proxy != nil {
				s.proxy.MarkFailure(fmt.Errorf("blocking detected: %s", attemptResult.blockResult.BlockType))
				s.logger.Info().
					Int("attempt", attempt).
					Msg("Current proxy marked as failed, next attempt will use different proxy")
			}

			// If this was the last attempt, fall back to HTTP
			if attempt == maxRetries {
				s.logger.Warn().
					Int("total_attempts", attempt+1).
					Msg("Phase 5: All retry attempts blocked, falling back to HTTP")

				return s.httpFallback(ctx, urlStr, scrapeCtx.userAgent, startTime)
			}

			// Otherwise, continue to next attempt with new proxy
			lastError = fmt.Errorf("blocking detected: %s", attemptResult.blockResult.BlockType)
			continue
		}

		// Success! Extract results
		s.logger.Info().
			Int("attempt", attempt).
			Msg("Phase 5: Scrape attempt successful")

		html = attemptResult.html
		screenshotData = attemptResult.screenshotData
		title = attemptResult.title
		finalURL = attemptResult.finalURL
		successfulAttempt = true

		// Mark proxy as successful if applicable
		if s.proxy != nil && s.proxy.IsEnabled() && scrapeCtx.proxy != nil {
			s.proxy.MarkSuccess()
			s.logger.Info().
				Msg("Proxy marked as successful")
		}

		// Break out of retry loop
		break
	}

	// If all attempts failed, fall back to HTTP
	if !successfulAttempt {
		s.logger.Warn().
			Err(lastError).
			Int("total_attempts", maxRetries+1).
			Msg("Phase 5: All scrape attempts failed, falling back to HTTP")

		// Use a default UA for HTTP fallback
		userAgent := opts.UserAgent
		if userAgent == "" && s.uaRotator != nil {
			userAgent = s.uaRotator.Get()
		}
		if userAgent == "" {
			userAgent = "MCP-Web-Scrape/1.0 (+https://github.com/metall/mcp-web-scrape)"
		}

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
			// Phase 1: Set User-Agent in JS context (navigator.userAgent)
			// Note: Full HTTP+JS UA sync via CDP requires complex CDP integration
			// Current approach: JS UA rotation + accept HTTP header limitation
			// This is acceptable for many sites; proxy rotation handles blocked IPs

			var result interface{}
			err := chromedp.Evaluate(fmt.Sprintf(`
				Object.defineProperty(navigator, 'userAgent', {
					get: function() { return %q; },
					configurable: true
				});
			`, userAgent), &result).Do(ctx)

			if err != nil {
				s.logger.Debug().Err(err).Msg("Failed to set JS User-Agent")
			} else {
				s.logger.Debug().
					Str("user_agent", userAgent).
					Msg("User-Agent set in JS context")
			}

			// Phase 3: Extended Stealth - Inject anti-detection scripts
			if stealth != nil {
				// Generate random fingerprint for this session
				fingerprint := stealth.GenerateRandomFingerprint()

				s.logger.Info().
					Str("timezone", fingerprint.Timezone).
					Str("language", fingerprint.Language).
					Str("platform", fingerprint.Platform).
					Str("webgl_vendor", fingerprint.WebGLVendor).
					Msg("Phase 3: Injecting Extended Stealth anti-detection scripts")

				// Inject comprehensive anti-detection scripts
				if err := stealth.InjectAntiDetectionScripts(fingerprint).Do(ctx); err != nil {
					s.logger.Warn().Err(err).Msg("Failed to inject anti-detection scripts (non-critical)")
				} else {
					s.logger.Info().Msg("✅ Extended Stealth scripts injected successfully")
				}
			}

			return nil
		}),
	}

	// Navigate with stealth if enabled
	// Use FAST navigation - load URL directly without waiting for full page load
	navigationTask := s.buildNavigationTask(urlStr, userAgent, stealth)
	tasks = append(tasks, navigationTask)

	// Wait for specific selector if provided
	if opts.WaitForSelector != "" {
		waitAction := chromedp.WaitVisible(opts.WaitForSelector, chromedp.ByQuery)
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

// buildNavigationTask создает единую задачу навигации для stealth/non-stealth режимов
func (s *ChromeScraper) buildNavigationTask(urlStr, userAgent string, stealth *browser.StealthActions) chromedp.Action {
	navigationAction := chromedp.ActionFunc(func(ctx context.Context) error {
		s.logger.Info().Msg("🌐 Starting FAST navigation...")
		startTime := time.Now()

		// Load URL directly without waiting for page load events
		var result interface{}
		if err := chromedp.Evaluate(fmt.Sprintf(`window.location.href = %q;`, urlStr), &result).Do(ctx); err != nil {
			s.logger.Error().Err(err).Msg("❌ URL change failed")
			return err
		}

		s.logger.Info().Dur("url_change_duration", time.Since(startTime)).Msg("✅ URL changed, waiting for body...")

		// Wait for body to appear using configurable polling
		startTime = time.Now()
		bodyFound := false

		// Use configurable polling parameters
		maxAttempts := s.browserCfg.PollingConfig.MaxAttempts
		interval := s.browserCfg.PollingConfig.Interval

		if maxAttempts == 0 {
			maxAttempts = 60 // default
		}
		if interval == 0 {
			interval = 100 * time.Millisecond // default
		}

		for i := 0; i < maxAttempts; i++ {
			var bodyExists bool
			err := chromedp.Evaluate(`(() => { return document.body !== null; })()`, &bodyExists).Do(ctx)
			if err == nil && bodyExists {
				bodyFound = true
				break
			}

			// Check context cancellation
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled while waiting for body")
			default:
				time.Sleep(interval)
			}
		}

		if !bodyFound {
			return fmt.Errorf("body not found after %d attempts (%v)", maxAttempts, time.Since(startTime))
		}

		s.logger.Info().Dur("wait_body_duration", time.Since(startTime)).Msg("✅ Body found")
		return nil
	})

	// Apply stealth wrapper if enabled
	if stealth != nil {
		return stealth.ApplyStealth(navigationAction)
	}
	return navigationAction
}

// httpFallback performs HTTP fallback when Chrome fails
func (s *ChromeScraper) httpFallback(ctx context.Context, urlStr, userAgent string, startTime time.Time) (*Result, error) {
	// Phase 4: TLS Fingerprinting - Use TLS-aware HTTP client instead of standard http.Client
	s.logger.Info().Msg("Phase 4: Using TLS-aware HTTP client with Chrome fingerprint")

	// Create TLS client with Chrome fingerprint
	tlsConfig := tlshttp.DefaultTLSClientConfig
	tlsConfig.RandomizeExtensions = true // Enable JA4 protection

	tlsClient, err := tlshttp.NewTLSClient(tlsConfig)
	if err != nil {
		s.logger.Warn().Err(err).Msg("Failed to create TLS client, falling back to standard HTTP")
		// Fallback to standard HTTP client
		tlsClient = nil
	}

	var client *http.Client
	if tlsClient != nil {
		client = tlsClient.GetHttpClient()

		// Log fingerprint info
		fingerprintInfo := tlsClient.GetFingerprintInfo()
		s.logger.Info().
			Interface("fingerprint", fingerprintInfo).
			Msg("TLS fingerprinting enabled")

		// Add proxy to TLS client if enabled
		if s.proxy != nil && s.proxy.IsEnabled() {
			// Get next proxy
			selectedProxy, proxyErr := s.proxy.GetNext()
			if proxyErr == nil && selectedProxy != nil {
				if err := tlsClient.SetProxy(selectedProxy.URL); err != nil {
					s.logger.Warn().Err(err).Msg("Failed to set proxy for TLS client")
				} else {
					s.logger.Info().
						Str("proxy", selectedProxy.URL).
						Msg("Using proxy with TLS fingerprinting")
				}
			}
		}
	} else {
		// Fallback to standard HTTP client
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}

		// Add proxy to standard HTTP client if enabled
		if s.proxy != nil && s.proxy.IsEnabled() {
			client.Transport = &http.Transport{
				Proxy: s.proxy.GetProxyFunc(),
			}
			s.logger.Info().
				Msg("Using proxy for standard HTTP fallback")
		}
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

