package tools

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
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
	cache       *cache.Cache
	browserPool *browser.Pool
	ragConfig   config.RAGConfig
	browserCfg  config.BrowserConfig
	uaRotator   *useragent.Rotator
	proxy       *proxy.Rotator
	converter   *converter.Converter
	githubCfg   config.GitHubConfig
	logger      zerolog.Logger
}

// scrapeContext holds the context for a single scrape attempt (Phase 5: Retry Loop)
type scrapeContext struct {
	browserCtx    context.Context
	browserCancel context.CancelFunc
	proxy         *proxy.Proxy
	userAgent     string
	stealth       *browser.StealthActions

	// Named session state. When useSession is true, browserCtx is a tab
	// derived from a persistent session context; browserCancel closes only
	// that tab (the session survives). sessionID identifies the named
	// session for close-after-scrape cleanup.
	useSession    bool
	sessionID     string
	sessionReused bool
}

// NewChromeScraper создает новый ChromeScraper
func NewChromeScraper(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, browserCfg config.BrowserConfig, uaRotator *useragent.Rotator, proxy *proxy.Rotator, githubCfg config.GitHubConfig) *ChromeScraper {
	return &ChromeScraper{
		cache:       cache,
		browserPool: browserPool,
		ragConfig:   ragConfig,
		browserCfg:  browserCfg,
		uaRotator:   uaRotator,
		proxy:       proxy,
		converter:   converter.New(),
		githubCfg:   githubCfg,
		logger:      logger.Get(),
	}
}

// createScrapeContext creates a new scrape context for a single attempt (Phase 5: Retry Loop)
// This method extracts browser context creation, proxy selection, and UA generation
func (s *ChromeScraper) createScrapeContext(ctx context.Context, urlStr string, opts Options) (*scrapeContext, error) {
	scrapeCtx := &scrapeContext{}

	// 1a. Named persistent session path: reuse a browser context that
	// survives across scrape calls (shared cookie jar / storage).
	// The session's chromedp context IS the browser context — navigations
	// happen directly in it. browserCancel is a no-op so the retry loop
	// does NOT close the session between attempts (only the explicit
	// close_session flag or TTL eviction closes it).
	if opts.SessionID != "" && s.browserPool != nil {
		sm := s.browserPool.Sessions()
		if sm != nil {
			sessCtx, err := sm.GetOrCreate(s.browserPool.Allocator(), opts.SessionID)
			if err != nil {
				return nil, fmt.Errorf("failed to get named session %q: %w", opts.SessionID, err)
			}
			scrapeCtx.browserCtx = sessCtx
			scrapeCtx.browserCancel = func() {} // no-op — session survives
			scrapeCtx.useSession = true
			scrapeCtx.sessionID = opts.SessionID
			scrapeCtx.sessionReused = true
			s.logger.Info().
				Str("session_id", opts.SessionID).
				Msg("Using named session for scrape")
		} else {
			s.logger.Warn().
				Str("session_id", opts.SessionID).
				Msg("Named sessions disabled (TTL=0); falling back to ephemeral context")
		}
	}

	// 1b. Ephemeral path (default): fresh browser context per call
	if !scrapeCtx.useSession {
		browserCtx, browserCancel, err := s.browserPool.GetContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get browser context: %w", err)
		}
		scrapeCtx.browserCtx = browserCtx
		scrapeCtx.browserCancel = browserCancel
	}

	// 2. Get User-Agent
	userAgent := opts.UserAgent
	if userAgent == "" && s.uaRotator != nil {
		userAgent = s.uaRotator.GetRandomDesktop()
	}
	if userAgent == "" {
		// Use real Chrome UA instead of MCP-Web-Scrape
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
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
	// isActionError indicates the error was caused by a user-supplied interactive
	// action (type/click/scroll_to/...) that failed on the page — NOT a network
	// or blocking issue. Such errors are deterministic: retrying with a new proxy
	// or browser context won't help, so the Phase 5 retry loop should skip them
	// and fall back to HTTP immediately instead of wasting cycles.
	isActionError bool
	// jsResults contains return values from all execute_js actions
	jsResults []browser.JSResult
}

// scrapeAttempt performs a single scrape attempt (Phase 5: Retry Loop)
// This method executes Chrome tasks and returns scrape result or blocking detection
func (s *ChromeScraper) scrapeAttempt(ctx context.Context, urlStr string, scrapeCtx *scrapeContext, opts Options) scrapeAttemptResult {
	result := scrapeAttemptResult{}

	// 1. Build Chrome tasks
	tasks, actionExecutor := s.buildChromeTasks(urlStr, scrapeCtx.userAgent, scrapeCtx.stealth, opts, scrapeCtx.useSession)

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
		// Classify action errors: "failed to execute actions: ..." comes from
		// ActionExecutor and means a user-supplied selector/interaction failed on
		// the page. This is deterministic — retrying with a new proxy won't help.
		if strings.Contains(chromeErr.Error(), "failed to execute actions") {
			result.isActionError = true
		}
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

	// Collect execute_js results from actionExecutor
	if actionExecutor != nil {
		result.jsResults = actionExecutor.GetJSResults()
	}

	return result
}


// Scrape реализует интерфейс Scraper
func (s *ChromeScraper) Scrape(ctx context.Context, urlStr string, opts Options) (*Result, error) {
	startTime := time.Now()

	s.logger.Info().
		Str("url", urlStr).
		Msg("🚀 Starting scrape for URL")

	// Apply tool-level timeout for scraping operations
	toolTimeout := s.browserCfg.ToolTimeout
	if toolTimeout == 0 {
		toolTimeout = 120 * time.Second // default timeout
	}

	toolCtx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	// 0. Validate browser pool (CRITICAL for Chrome scraper)
	if s.browserPool == nil {
		return nil, &ScrapeError{
			Code:     "browser_not_available",
			Message:  "Browser pool is not initialized - Chrome scraping is not available in this environment",
			Hints:    []string{"use_http_scraper"},
			CanRetry: false,
		}
	}

	// 1. Validate URL
	if _, err := ValidateURL(urlStr); err != nil {
		return nil, &ScrapeError{
			Code:     "invalid_url",
			Message:  err.Error(),
			Hints:    []string{},
			CanRetry: false,
		}
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

	// 2.5. GitHub HTTP Fallback (before Chrome retry loop)
	// GitHub uses advanced bot detection that defeats Chrome headless scraping
	// Use special GitHub endpoints that work without authentication
	// 2.5. Platform HTTP Fallback (before Chrome retry loop)
	// GitHub, GitLab, and Gitea use advanced bot detection that defeats Chrome headless scraping
	// Use special API endpoints that work without authentication
	isGitHub := strings.Contains(urlStr, "github.com")
	isGitea := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`).MatchString(urlStr)
	isGitLab := strings.Contains(urlStr, "gitlab.com") || regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`).MatchString(urlStr)

	s.logger.Info().
		Str("url", urlStr).
		Bool("is_github", isGitHub).
		Bool("is_gitea", isGitea).
		Bool("is_gitlab", isGitLab).
		Bool("has_actions", hasActions).
		Msg("🔍 Platform detection debug")

	if (isGitHub || isGitea || isGitLab) && !hasActions {
		platform := "GitHub"
		if isGitea {
			platform = "Gitea"
		} else if isGitLab {
			platform = "GitLab"
		}

		s.logger.Info().
			Str("url", urlStr).
			Str("platform", platform).
			Msg("🎯 Platform detected - using intelligent API mode")

		// Check if smart catalog mode is requested
		if strings.Contains(urlStr, "?mode=catalog") || strings.Contains(urlStr, "&mode=catalog") {
			s.logger.Info().Msg("📋 Smart catalog mode: Building release catalog for intelligent LLM selection")

			catalog, err := s.githubSmartCatalog(urlStr)
			if err != nil {
				return nil, &ScrapeError{
					Code:     "catalog_error",
					Message:  fmt.Sprintf("GitHub catalog failed: %v", err),
					Hints:    []string{"retry", "try_without_catalog"},
					CanRetry: true,
				}
			}

			// Convert catalog to markdown
			catalogMD := s.buildCatalogMarkdown(catalog)

			s.logger.Info().
				Int("releases_total", catalog.TotalCount).
				Int("catalog_size", len(catalogMD)).
				Msg("GitHub release catalog created for LLM selection")

			return &Result{
				HTML:        catalogMD,
				URL:         urlStr,
				FinalURL:    urlStr,
				StatusCode:  200,
				ContentType: "text/markdown",
				Duration:    time.Since(startTime),
				SizeBytes:   len(catalogMD),
				Format:      "markdown",
				FromCache:   false,
				Method:      "GitHub Catalog",
			}, nil
		}

		// Standard mode: Convert to API with flexible results
		platformURL := s.convertPlatformURL(urlStr)
		s.logger.Info().
			Str("original_url", urlStr).
			Str("platform_url", platformURL).
			Msg("🔄 Converted platform URL to API endpoint")

		// Get User-Agent for HTTP fallback
		userAgent := opts.UserAgent
		if userAgent == "" && s.uaRotator != nil {
			userAgent = s.uaRotator.GetRandomDesktop()
		}
		if userAgent == "" {
			userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
		}

		return s.platformAPIFallback(toolCtx, platformURL, userAgent, startTime)
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
	var jsResults []browser.JSResult

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

		// CRITICAL: Clean up browser context at the end of each iteration
		// NOT using defer here to prevent resource accumulation in retry loop
		cleanupNeeded := true
		defer func() {
			if cleanupNeeded {
				scrapeCtx.browserCancel()
			}
		}()

		// Log active tabs before attempt
		activeTabs := s.browserPool.GetActiveTabs()
		s.logger.Debug().
			Int("attempt", attempt).
			Int32("active_tabs", activeTabs).
			Msg("Starting scrape attempt")

		// Perform scrape attempt
		attemptResult := s.scrapeAttempt(toolCtx, urlStr, scrapeCtx, opts)

		// Handle result
		if attemptResult.err != nil {
			// Chrome execution error
			s.logger.Warn().
				Int("attempt", attempt).
				Err(attemptResult.err).
				Msg("Scrape attempt failed with error")

			// Action errors (failed to execute user-supplied actions) are
			// deterministic — retrying with a new proxy/browser won't fix a
			// wrong selector or a missing element. Skip Phase 5 retry entirely
			// and fall back to HTTP immediately.
			if attemptResult.isActionError {
				s.logger.Warn().
					Int("attempt", attempt).
					Msg("Action error detected — skipping Phase 5 retry, falling back to HTTP")

				// Mark proxy as failed if applicable (same as regular error path)
				if s.proxy != nil && s.proxy.IsEnabled() && scrapeCtx.proxy != nil {
					s.proxy.MarkFailure(attemptResult.err)
				}

				lastError = attemptResult.err
				// Clean up before falling back
				scrapeCtx.browserCancel()
				cleanupNeeded = false
				return s.httpFallback(ctx, urlStr, scrapeCtx.userAgent, startTime)
			}

			// Mark proxy as failed if applicable
			if s.proxy != nil && s.proxy.IsEnabled() && scrapeCtx.proxy != nil {
				s.proxy.MarkFailure(attemptResult.err)
			}

			lastError = attemptResult.err
			// Clean up before continuing to next iteration
			scrapeCtx.browserCancel()
			cleanupNeeded = false
			s.logger.Debug().
				Int("attempt", attempt).
				Int32("active_tabs", s.browserPool.GetActiveTabs()).
				Msg("Cleanup after Chrome error")
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

				// Clean up before returning
				scrapeCtx.browserCancel()
				cleanupNeeded = false
				s.logger.Debug().
					Int("attempt", attempt).
					Int32("active_tabs", s.browserPool.GetActiveTabs()).
					Msg("Cleanup before HTTP fallback")
				return s.httpFallback(ctx, urlStr, scrapeCtx.userAgent, startTime)
			}

			// Otherwise, continue to next attempt with new proxy
			lastError = fmt.Errorf("blocking detected: %s", attemptResult.blockResult.BlockType)
			// Clean up before continuing to next iteration
			scrapeCtx.browserCancel()
			cleanupNeeded = false
			s.logger.Debug().
				Int("attempt", attempt).
				Int32("active_tabs", s.browserPool.GetActiveTabs()).
				Msg("Cleanup after blocking detection")
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
		jsResults = attemptResult.jsResults
		successfulAttempt = true

		// Mark proxy as successful if applicable
		if s.proxy != nil && s.proxy.IsEnabled() && scrapeCtx.proxy != nil {
			s.proxy.MarkSuccess()
			s.logger.Info().
				Msg("Proxy marked as successful")
		}

		// Clean up before breaking out of loop
		scrapeCtx.browserCancel()
		cleanupNeeded = false
		s.logger.Debug().
			Int("attempt", attempt).
			Int32("active_tabs", s.browserPool.GetActiveTabs()).
			Msg("Cleanup after successful scrape")
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
			userAgent = s.uaRotator.GetRandomDesktop()
		}
		if userAgent == "" {
			userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
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
		JSResults:       jsResults,
		Method:          s.Name(),
	}

	// Named session post-processing
	if opts.SessionID != "" {
		result.SessionReused = true
		s.logger.Info().
			Str("session_id", opts.SessionID).
			Bool("session_reused", true).
			Msg("Scrape completed using named session")

		// Explicit session close after a successful scrape
		if opts.CloseSession && s.browserPool != nil {
			if sm := s.browserPool.Sessions(); sm != nil {
				if sm.Close(opts.SessionID) {
					s.logger.Info().
						Str("session_id", opts.SessionID).
						Msg("Named session closed (close_session=true)")
				}
			}
		}
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
// Возвращает tasks и actionExecutor (для извлечения результатов execute_js)
// preserveSession: когда true (named session), очистка cookies/storage пропускается
func (s *ChromeScraper) buildChromeTasks(urlStr, userAgent string, stealth *browser.StealthActions, opts Options, preserveSession bool) ([]chromedp.Action, *browser.ActionExecutor) {
	tasks := []chromedp.Action{
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Phase 1: Set User-Agent in JS context (navigator.userAgent)
			// NOTE: HTTP User-Agent is set at allocator level during browser pool creation
			// This sync ensures JS navigator.userAgent matches HTTP headers.
			//
			// Uses page.AddScriptToEvaluateOnNewDocument so the override
			// PERSISTS across navigations. Previously chromedp.Evaluate ran
			// on about:blank and the override was lost after Navigate().
			uaScript := fmt.Sprintf(`
				Object.defineProperty(navigator, 'userAgent', {
					get: function() { return %q; },
					configurable: true
				});
			`, userAgent)

			if _, err := page.AddScriptToEvaluateOnNewDocument(uaScript).Do(ctx); err != nil {
				s.logger.Debug().Err(err).Msg("Failed to set JS User-Agent")
			} else {
				s.logger.Debug().
					Str("user_agent", userAgent).
					Msg("User-Agent synchronized in JS context")
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
	navigationTask := s.buildNavigationTask(urlStr, userAgent, stealth, preserveSession)
	tasks = append(tasks, navigationTask)

	// Wait for specific selector if provided — wrapped in timeout to prevent indefinite hangs
	if opts.WaitForSelector != "" {
		waitAction := chromedp.ActionFunc(func(ctx context.Context) error {
			timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			wait := chromedp.WaitVisible(opts.WaitForSelector, chromedp.ByQuery)
			if stealth != nil {
				wait = stealth.ApplyStealth(wait)
			}

			err := wait.Do(timeoutCtx)
			if err != nil {
				s.logger.Warn().
					Str("selector", opts.WaitForSelector).
					Err(err).
					Msg("WaitVisible timed out — continuing with page content")
				// Graceful degradation: log and continue rather than failing the scrape
				return nil
			}

			s.logger.Debug().
				Str("selector", opts.WaitForSelector).
				Msg("WaitVisible: element found")
			return nil
		})
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
	var actionExecutor *browser.ActionExecutor
	if len(opts.Actions) > 0 {
		actionExecutor = browser.NewActionExecutor(s.logger, stealth)
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			s.logger.Info().
				Int("actions_count", len(opts.Actions)).
				Msg("Executing interactive actions")

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

	return tasks, actionExecutor
}

// buildNavigationTask создает единую задачу навигации для stealth/non-stealth режимов
// preserveSession: когда false, перед навигацией очищаются все cookies/storage
// (защита от GitHub "signed in with another tab"). Когда true (named session),
// очистка пропускается — логин из предыдущих вызовов сохраняется.
func (s *ChromeScraper) buildNavigationTask(urlStr, userAgent string, stealth *browser.StealthActions, preserveSession bool) chromedp.Action {
	navigationAction := chromedp.ActionFunc(func(ctx context.Context) error {
		s.logger.Info().Msg("🌐 Starting navigation...")
		startTime := time.Now()

		// CRITICAL: Clear ALL session data before navigation to prevent session conflicts
		// This fixes GitHub "signed in with another tab" errors by:
		// 1. Clearing all cookies
		// 2. Clearing localStorage
		// 3. Clearing sessionStorage
		//
		// SKIPPED when preserveSession is true (named persistent session) —
		// clearing would destroy the login state the session is meant to carry.
		if !preserveSession {
			if err := chromedp.ActionFunc(func(ctx context.Context) error {
			// Clear all cookies via CDP
			if err := network.ClearBrowserCookies().Do(ctx); err != nil {
				s.logger.Debug().Err(err).Msg("Failed to clear browser cookies (non-critical)")
			} else {
				s.logger.Debug().Msg("✅ Cleared browser cookies")
			}

			// Clear localStorage and sessionStorage via JS
			var result interface{}
			if err := chromedp.Evaluate(`
				(() => {
					// Clear localStorage
					try {
						localStorage.clear();
					} catch(e) {
						console.log('Failed to clear localStorage:', e);
					}

					// Clear sessionStorage
					try {
						sessionStorage.clear();
					} catch(e) {
						console.log('Failed to clear sessionStorage:', e);
					}

					// Clear all cookies via JS for current domain
					document.cookie.split(";").forEach(function(c) {
						document.cookie = c.trim().split("=")[0] +
							'=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/';
					});

					return true;
				})()
			`, &result).Do(ctx); err != nil {
				s.logger.Debug().Err(err).Msg("Failed to clear storage via JS (non-critical)")
			} else {
				s.logger.Debug().Msg("✅ Cleared localStorage/sessionStorage/cookies")
			}

			return nil
		}).Do(ctx); err != nil {
			s.logger.Debug().Err(err).Msg("Failed to execute storage clearing (non-critical)")
		}

		// Add small delay to ensure storage is fully cleared before navigation
		time.Sleep(100 * time.Millisecond)
		} else {
			s.logger.Debug().Msg("Skipping cookie/storage clearing (named session)")
		}

		// Navigate with timeout to prevent hanging on slow sites
		// chromedp.Navigate waits for full page load which can take 60+ seconds
		navTimeout := 30 * time.Second
		navCtx, navCancel := context.WithTimeout(ctx, navTimeout)
		defer navCancel()

		navStart := time.Now()
		if err := chromedp.Navigate(urlStr).Do(navCtx); err != nil {
			if navCtx.Err() == context.DeadlineExceeded {
				s.logger.Error().
					Dur("nav_timeout", navTimeout).
					Dur("actual_duration", time.Since(navStart)).
					Msg("❌ Navigation timeout - site too slow or unresponsive")
				return fmt.Errorf("navigation timeout after %v", navTimeout)
			}
			s.logger.Error().Err(err).Msg("❌ Navigation failed")
			return err
		}

		s.logger.Info().Dur("nav_duration", time.Since(navStart)).Dur("url_change_duration", time.Since(startTime)).Msg("✅ Navigation complete")

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

// convertGitHubURL converts GitHub URLs to API endpoints for better scraping
// Supports flexible optimization through query parameters:
// - Default: last 5 releases (balanced)
// - ?releases=10: last 10 releases (detailed)
// - ?releases=all: all releases (expensive but complete)
// - Issues/Pulls search: converted to GitHub Search API
// Examples:
// - https://github.com/owner/repo/releases → https://api.github.com/repos/owner/repo/releases?per_page=5
// - https://github.com/owner/repo/releases?releases=10 → https://api.github.com/repos/owner/repo/releases?per_page=10
// - https://github.com/owner/repo/issues?q=is:issue+term → https://api.github.com/search/issues?q=repo:owner/repo+is:issue+term
// - https://github.com/owner/repo/commit/sha → https://api.github.com/repos/owner/repo/commits/sha
func (s *ChromeScraper) convertGitHubURL(urlStr string) string {
	// GitHub releases page → API with flexible result count
	if strings.Contains(urlStr, "/releases") && !strings.Contains(urlStr, ".atom") {
		if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/releases`).FindStringSubmatch(urlStr); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]

			// Check if user specified release count in URL
			releaseCount := "5" // Default: balanced approach
			if matches := regexp.MustCompile(`[?&]releases=(\d+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
				releaseCount = matches[1]
			} else if matches := regexp.MustCompile(`[?&]releases=all`).FindStringSubmatch(urlStr); len(matches) > 0 {
				releaseCount = "100" // Get all available releases
			}

			return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=%s", owner, repo, releaseCount)
		}
	}

	// GitHub issues/pulls search → Search API
	if (strings.Contains(urlStr, "/issues?") || strings.Contains(urlStr, "/pulls?")) &&
		strings.Contains(urlStr, "q=") {
		if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/(issues|pulls)\?q=([^&]+)(?:&.*)?$`).FindStringSubmatch(urlStr); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			itemType := matches[3] // "issues" or "pulls"
			query := matches[4]

			// Build search query for GitHub API
			// Convert URL query format to API format
			searchQuery := fmt.Sprintf("repo:%s/%s+%s", owner, repo, query)

			return fmt.Sprintf("https://api.github.com/search/%s?q=%s&per_page=10", itemType, searchQuery)
		}
	}

	// GitHub commit page → API
	if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/commit/([a-f0-9]+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		sha := matches[3]
		return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, sha)
	}

	// GitHub commits list page → API
	if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/commits/([^/]+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		branch := matches[3]
		return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?sha=%s&per_page=10", owner, repo, branch)
	}

	// GitHub repo page → API
	if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/?$`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	}

	return urlStr
}

// convertGitLabURL converts GitLab URLs to API endpoints for better scraping
// GitLab API uses URL-encoded project paths (owner%2Frepo format)
// Examples:
// - https://gitlab.com/owner/repo → https://gitlab.com/api/v4/projects/owner%2Frepo
// - https://gitlab.com/owner/repo/-/commit/sha → https://gitlab.com/api/v4/projects/owner%2Frepo/repository/commits/sha
// - https://gitlab.com/owner/repo/-/releases → https://gitlab.com/api/v4/projects/owner%2Frepo/releases
func (s *ChromeScraper) convertGitLabURL(urlStr string) string {
	// Extract base domain for self-hosted GitLab instances
	baseDomain := "gitlab.com"
	if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
		if matches[1] != "gitlab.com" {
			baseDomain = matches[1]
		}
	}

	// Helper function to encode project path for GitLab API
	encodeProjectPath := func(owner, repo string) string {
		return fmt.Sprintf("%s%%2F%s", owner, repo) // URL encode slash
	}

	// Remove domain to get path (for proper regex matching)
	path := urlStr
	if matches := regexp.MustCompile(`https://[^/]+(/.*)`).FindStringSubmatch(urlStr); len(matches) > 0 {
		path = matches[1]
	}

	// GitLab commit page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/-/commit/([a-f0-9]+)`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		sha := matches[3]
		projectPath := encodeProjectPath(owner, repo)
		return fmt.Sprintf("https://%s/api/v4/projects/%s/repository/commits/%s", baseDomain, projectPath, sha)
	}

	// GitLab releases page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/-/releases`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		projectPath := encodeProjectPath(owner, repo)
		return fmt.Sprintf("https://%s/api/v4/projects/%s/releases", baseDomain, projectPath)
	}

	// GitLab issues page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/-/issues`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		projectPath := encodeProjectPath(owner, repo)
		return fmt.Sprintf("https://%s/api/v4/projects/%s/issues", baseDomain, projectPath)
	}

	// GitLab merge requests page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/-/merge_requests`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		projectPath := encodeProjectPath(owner, repo)
		return fmt.Sprintf("https://%s/api/v4/projects/%s/merge_requests", baseDomain, projectPath)
	}

	// GitLab repo page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/?$`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		projectPath := encodeProjectPath(owner, repo)
		return fmt.Sprintf("https://%s/api/v4/projects/%s", baseDomain, projectPath)
	}

	return urlStr
}

// convertGiteaURL converts Gitea URLs to API endpoints for better scraping
// Gitea API is similar to GitHub but with some differences
// Examples:
// - https://gitea.com/owner/repo → https://gitea.com/api/v1/repos/owner/repo
// - https://gitea.com/owner/repo/commit/sha → https://gitea.com/api/v1/repos/owner/repo/git/commits/sha
// - https://gitea.com/owner/repo/releases → https://gitea.com/api/v1/repos/owner/repo/releases
func (s *ChromeScraper) convertGiteaURL(urlStr string) string {
	// Extract base domain for self-hosted Gitea instances
	baseDomain := "gitea.com"
	if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
		if matches[1] != "gitea.com" && matches[1] != "gitea.io" {
			baseDomain = matches[1]
		}
	}

	// Remove domain to get path (for proper regex matching)
	path := urlStr
	if matches := regexp.MustCompile(`https://[^/]+(/.*)`).FindStringSubmatch(urlStr); len(matches) > 0 {
		path = matches[1]
	}

	// Gitea commit page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/commit/([a-f0-9]+)`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		sha := matches[3]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/git/commits/%s", baseDomain, owner, repo, sha)
	}

	// Gitea commits list → API (check before generic repo pattern)
	// Matches: /commits, /commits/, /commits/master, /commits/main
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/commits/?([^/]+)?`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		branch := matches[3]
		if branch == "" {
			branch = "master" // default branch
		}
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/commits?sha=%s&limit=10", baseDomain, owner, repo, branch)
	}

	// Gitea releases page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/releases`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/releases", baseDomain, owner, repo)
	}

	// Gitea issues page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/issues`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/issues", baseDomain, owner, repo)
	}

	// Gitea pulls page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/pulls`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/pulls", baseDomain, owner, repo)
	}

	// Gitea repo page → API (check LAST, as it's most generic)
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/?$`).FindStringSubmatch(path); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s", baseDomain, owner, repo)
	}

	return urlStr
}

// convertPlatformURL is a unified function that detects the platform and converts URLs to API endpoints
// Supports GitHub, GitLab, and Gitea (including self-hosted instances)
func (s *ChromeScraper) convertPlatformURL(urlStr string) string {
	// Detect platform and call appropriate conversion function
	if strings.Contains(urlStr, "github.com") {
		return s.convertGitHubURL(urlStr)
	}

	if strings.Contains(urlStr, "gitlab.com") || regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`).MatchString(urlStr) {
		return s.convertGitLabURL(urlStr)
	}

	// Check for common Gitea instances or Gitea-specific URL patterns
	// This includes official Gitea (gitea.com, gitea.io) and self-hosted instances
	if strings.Contains(urlStr, "gitea.com") || strings.Contains(urlStr, "gitea.io") ||
		regexp.MustCompile(`gitea\.[^/]+`).MatchString(urlStr) || // Matches gitea.example.com
		regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/(issues|pulls|releases|commit)`).MatchString(urlStr) {
		return s.convertGiteaURL(urlStr)
	}

	return urlStr
}

// httpFallback performs HTTP fallback when Chrome fails
func (s *ChromeScraper) httpFallback(ctx context.Context, urlStr, userAgent string, startTime time.Time) (*Result, error) {
	// Phase 4: HTTP Fallback - Use standard HTTP client for better compatibility
	// For GitHub, we use standard HTTP client to avoid TLS fingerprinting issues
	isGitHub := strings.Contains(urlStr, "github.com")

	if isGitHub {
		s.logger.Info().Msg("Phase 4: Using standard HTTP client for GitHub (avoiding TLS fingerprinting)")
	} else {
		s.logger.Info().Msg("Phase 4: Using TLS-aware HTTP client with Chrome fingerprint")
	}

	var client *http.Client

	if isGitHub {
		// Use standard HTTP client for GitHub to avoid TLS fingerprinting detection
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
			s.logger.Info().Msg("Using proxy for GitHub HTTP client")
		}
	} else {
		// Use TLS-aware client for non-GitHub sites
		tlsConfig := tlshttp.DefaultTLSClientConfig
		tlsConfig.RandomizeExtensions = true // Enable JA4 protection

		tlsClient, err := tlshttp.NewTLSClient(tlsConfig)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to create TLS client, falling back to standard HTTP")
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
			}
		} else {
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
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, &ScrapeError{
			Code:     "request_error",
			Message:  fmt.Sprintf("Failed to create HTTP request: %v", err),
			Hints:    []string{},
			CanRetry: false,
		}
	}

	// Set realistic browser headers
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// For GitHub, don't request compression to avoid decompression issues
	if !isGitHub {
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	}

	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := client.Do(req)
	if err != nil {
		// TLS client failed - try simple HTTPScraper as last resort
		s.logger.Warn().Err(err).Msg("TLS client failed, trying simple HTTP client")

		// Use existing HTTPScraper for compatibility
		httpScraper := NewHTTPScraper(s.cache, s.uaRotator, s.proxy)
		result, scrapeErr := httpScraper.Scrape(ctx, urlStr, Options{
			Timeout:    30 * time.Second,
			UserAgent: userAgent,
		})

		if scrapeErr != nil {
			if s.proxy != nil && s.proxy.IsEnabled() {
				s.proxy.MarkFailure(scrapeErr)
			}
			// Extract ScrapeError details for better error message
			var httpScrapeErr *ScrapeError
			hints := []string{"try_screenshot"}
			if errors.As(scrapeErr, &httpScrapeErr) {
				hints = append(hints, httpScrapeErr.Hints...)
			}
			return nil, &ScrapeError{
				Code:     "http_fallback_failed",
				Message:  fmt.Sprintf("Both TLS and simple HTTP failed: TLS=%v, Simple=%v", err, scrapeErr.Error()),
				Hints:    hints,
				CanRetry: false, // Already tried both methods
			}
		}

		// Success with simple HTTP
		s.logger.Info().
			Int("size", len(result.HTML)).
			Msg("✅ Successfully scraped with simple HTTP fallback")

		result.Method = "HTTP (simple fallback)"
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if s.proxy != nil && s.proxy.IsEnabled() {
			s.proxy.MarkFailure(fmt.Errorf("HTTP %d", resp.StatusCode))
		}
		return nil, &ScrapeError{
			Code:     "http_error",
			Message:  fmt.Sprintf("HTTP fallback failed with status %d", resp.StatusCode),
			Hints:    []string{"retry", "check_url"},
			CanRetry: true,
		}
	}

	// Mark proxy as successful
	if s.proxy != nil && s.proxy.IsEnabled() {
		s.proxy.MarkSuccess()
	}

	// Handle gzip compression for GitHub responses
	var bodyReader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to create gzip reader, trying uncompressed read")
		} else {
			defer gzipReader.Close()
			bodyReader = gzipReader
			s.logger.Debug().Msg("Decompressing gzipped response")
		}
	}

	body, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, &ScrapeError{
			Code:     "read_error",
			Message:  fmt.Sprintf("Failed to read HTTP response: %v", err),
			Hints:    []string{"retry"},
			CanRetry: true,
		}
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

// platformAPIFallback performs optimized platform API scraping for GitHub, GitLab, and Gitea
func (s *ChromeScraper) platformAPIFallback(ctx context.Context, urlStr, userAgent string, startTime time.Time) (*Result, error) {
	// Detect platform from URL
	var platform, acceptHeader, authPrefix string

	if strings.Contains(urlStr, "api.github.com") {
		platform = "GitHub"
		acceptHeader = "application/vnd.github.v3+json"
		authPrefix = "token"
	} else if strings.Contains(urlStr, "gitlab.com") || strings.Contains(urlStr, "/api/v4/") {
		platform = "GitLab"
		acceptHeader = "application/json"
		authPrefix = "Bearer"
	} else if strings.Contains(urlStr, "gitea.com") || strings.Contains(urlStr, "gitea.io") || strings.Contains(urlStr, "/api/v1/") {
		platform = "Gitea"
		acceptHeader = "application/json"
		authPrefix = "token"
	} else {
		// Default to GitHub for backwards compatibility
		platform = "GitHub"
		acceptHeader = "application/vnd.github.v3+json"
		authPrefix = "token"
	}

	s.logger.Info().Str("platform", platform).Msg("Phase 4: Using platform API with JSON response")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, &ScrapeError{
			Code:     "request_error",
			Message:  fmt.Sprintf("Failed to create %s API request: %v", platform, err),
			Hints:    []string{},
			CanRetry: false,
		}
	}

	// Set headers for platform API
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", acceptHeader)

	// Add platform token if available
	var token string
	switch platform {
	case "GitHub":
		token = s.githubCfg.Token
	case "GitLab":
		// GitLab token support could be added here if needed
		// token = s.gitlabCfg.Token
	case "Gitea":
		// Gitea token support could be added here if needed
		// token = s.giteaCfg.Token
	}

	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("%s %s", authPrefix, token))
		s.logger.Debug().Str("platform", platform).Msg("Using platform token for API request")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, &ScrapeError{
			Code:     "api_timeout",
			Message:  fmt.Sprintf("%s API request failed: %v", platform, err),
			Hints:    []string{"retry", "try_screenshot"},
			CanRetry: true,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, &ScrapeError{
			Code:     "api_error",
			Message:  fmt.Sprintf("%s API failed with status %d", platform, resp.StatusCode),
			Hints:    []string{"retry"},
			CanRetry: true,
		}
	}

	// Handle gzip compression
	var bodyReader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to create gzip reader")
		} else {
			defer gzipReader.Close()
			bodyReader = gzipReader
		}
	}

	body, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, &ScrapeError{
			Code:     "read_error",
			Message:  fmt.Sprintf("Failed to read %s API response: %v", platform, err),
			Hints:    []string{"retry"},
			CanRetry: true,
		}
	}

	s.logger.Info().
		Int("raw_size", len(body)).
		Str("platform", platform).
		Msg("Platform API response received")

	// Detect response type: search results, releases, or other
	var markdown string
	var methodLabel string

	// Try to parse as search results (contains "items" field)
	var searchResults map[string]interface{}
	if err := json.Unmarshal(body, &searchResults); err == nil {
		if items, ok := searchResults["items"].([]interface{}); ok {
			// This is a search response
			if platform == "GitHub" {
				markdown = s.convertGitHubSearchToMarkdown(items)
			} else {
				markdown = s.convertGenericSearchToMarkdown(items)
			}
			methodLabel = fmt.Sprintf("%s Search API", platform)

			s.logger.Info().
				Int("results_count", len(items)).
				Int("markdown_size", len(markdown)).
				Str("platform", platform).
				Msg("Search results converted to Markdown")

			return &Result{
				HTML:        markdown,
				URL:         urlStr,
				FinalURL:    urlStr,
				StatusCode:  resp.StatusCode,
				ContentType: "text/markdown",
				Duration:    time.Since(startTime),
				SizeBytes:   len(markdown),
				Format:      "markdown",
				FromCache:   false,
				Method:      methodLabel,
			}, nil
		}
	}

	// Try to parse as commit object (single commit)
	var commitObject map[string]interface{}
	if err := json.Unmarshal(body, &commitObject); err == nil {
		// Check if this looks like a commit object (has "sha" and "commit" fields)
		if _, hasSha := commitObject["sha"]; hasSha {
			if _, hasCommit := commitObject["commit"]; hasCommit {
				markdown = s.convertCommitToMarkdown(commitObject)
				methodLabel = fmt.Sprintf("%s Commit API", platform)

				s.logger.Info().
					Str("platform", platform).
					Int("markdown_size", len(markdown)).
					Msg("Single commit converted to Markdown")

				return &Result{
					HTML:        markdown,
					URL:         urlStr,
					FinalURL:    urlStr,
					StatusCode:  resp.StatusCode,
					ContentType: "text/markdown",
					Duration:    time.Since(startTime),
					SizeBytes:   len(markdown),
					Format:      "markdown",
					FromCache:   false,
					Method:      methodLabel,
				}, nil
			}
		}
	}

	// Try to parse as commits list (array)
	var commitsList []map[string]interface{}
	if err := json.Unmarshal(body, &commitsList); err == nil {
		// Check if first item looks like a commit (has "sha" field)
		if len(commitsList) > 0 {
			if _, hasSha := commitsList[0]["sha"]; hasSha {
				markdown = s.convertCommitsListToMarkdown(commitsList)
				methodLabel = fmt.Sprintf("%s Commits API", platform)

				s.logger.Info().
					Int("commits_count", len(commitsList)).
					Int("markdown_size", len(markdown)).
					Str("platform", platform).
					Msg("Commits list converted to Markdown")

				return &Result{
					HTML:        markdown,
					URL:         urlStr,
					FinalURL:    urlStr,
					StatusCode:  resp.StatusCode,
					ContentType: "text/markdown",
					Duration:    time.Since(startTime),
					SizeBytes:   len(markdown),
					Format:      "markdown",
					FromCache:   false,
					Method:      methodLabel,
				}, nil
			}
		}
	}

	// Try to parse as repo object (single repository)
	var repoObject map[string]interface{}
	if err := json.Unmarshal(body, &repoObject); err == nil {
		// Check if this looks like a repo object (has "name" and "owner" but NOT "sha" or "message")
		if _, hasName := repoObject["name"]; hasName {
			if _, hasOwner := repoObject["owner"]; hasOwner {
				if _, noSha := repoObject["sha"]; !noSha {
					if _, noMessage := repoObject["message"]; !noMessage {
						markdown = s.convertRepoToMarkdown(repoObject)
						methodLabel = fmt.Sprintf("%s Repo API", platform)

						s.logger.Info().
							Str("platform", platform).
							Int("markdown_size", len(markdown)).
							Msg("Repository object converted to Markdown")

						return &Result{
							HTML:        markdown,
							URL:         urlStr,
							FinalURL:    urlStr,
							StatusCode:  resp.StatusCode,
							ContentType: "text/markdown",
							Duration:    time.Since(startTime),
							SizeBytes:   len(markdown),
							Format:      "markdown",
							FromCache:   false,
							Method:      methodLabel,
						}, nil
					}
				}
			}
		}
	}

	// Try to parse as releases (array)
	var releases []map[string]interface{}
	if err := json.Unmarshal(body, &releases); err != nil {
		s.logger.Warn().Err(err).Str("platform", platform).Msg("Failed to parse API JSON as releases, returning raw JSON")
		// If parsing fails, return the raw JSON
		return &Result{
			HTML:        string(body),
			URL:         urlStr,
			FinalURL:    urlStr,
			StatusCode:  resp.StatusCode,
			ContentType: "application/json",
			Duration:    time.Since(startTime),
			SizeBytes:   len(body),
			Format:      "json",
			FromCache:   false,
			Method:      fmt.Sprintf("%s API", platform),
		}, nil
	}

	// Convert platform API releases to Markdown
	if platform == "GitHub" {
		markdown = s.convertGitHubReleasesToMarkdown(releases)
	} else {
		markdown = s.convertGenericReleasesToMarkdown(releases)
	}
	methodLabel = fmt.Sprintf("%s API", platform)

	s.logger.Info().
		Int("releases_count", len(releases)).
		Int("markdown_size", len(markdown)).
		Str("platform", platform).
		Msg("Platform API releases converted to Markdown")

	return &Result{
		HTML:        markdown,
		URL:         urlStr,
		FinalURL:    urlStr,
		StatusCode:  resp.StatusCode,
		ContentType: "text/markdown",
		Duration:    time.Since(startTime),
		SizeBytes:   len(markdown),
		Format:      "markdown",
		FromCache:   false,
		Method:      methodLabel,
	}, nil
}

// convertGitHubReleasesToMarkdown converts GitHub API releases to Markdown format
// Supports smart optimization based on release count
func (s *ChromeScraper) convertGitHubReleasesToMarkdown(releases []map[string]interface{}) string {
	if len(releases) == 0 {
		return "# No releases found"
	}

	// Adaptive release notes length based on total releases
	maxBodyLength := 500 // Default for 5 releases
	if len(releases) > 5 {
		maxBodyLength = 200 // Shorter for many releases
	}
	if len(releases) > 10 {
		maxBodyLength = 100 // Very short for many releases
	}
	if len(releases) <= 3 {
		maxBodyLength = 1000 // Longer for few releases
	}

	var markdown strings.Builder

	// Add optimization warning if many releases
	if len(releases) > 10 {
		markdown.WriteString(fmt.Sprintf("> ⚠️ **Token Optimization:** Showing %d releases (release notes truncated to %d chars each)\n\n", len(releases), maxBodyLength))
	} else if len(releases) > 5 {
		markdown.WriteString(fmt.Sprintf("> 📊 **Detailed View:** Showing %d releases\n\n", len(releases)))
	}

	markdown.WriteString(fmt.Sprintf("# Latest %d Releases\n\n", len(releases)))

	for i, release := range releases {
		// Release number (reverse order for display)
		releaseNum := len(releases) - i
		markdown.WriteString(fmt.Sprintf("## Release %d: %s\n\n", releaseNum, release["name"]))

		// Tag name
		if tag, ok := release["tag_name"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Tag:** `%s`\n\n", tag))
		}

		// Release date
		if date, ok := release["published_at"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Published:** %s\n\n", date[:10]))
		}

		// Author
		if author, ok := release["author"].(map[string]interface{}); ok {
			if login, ok := author["login"].(string); ok {
				markdown.WriteString(fmt.Sprintf("**Author:** @%s\n\n", login))
			}
		}

		// Release notes/body (with adaptive length)
		if body, ok := release["body"].(string); ok && body != "" {
			if len(body) > maxBodyLength {
				body = body[:maxBodyLength] + "..."
				markdown.WriteString(fmt.Sprintf("**Release Notes** (truncated):\\n\\n%s\\n\\n", body))
			} else {
				markdown.WriteString(fmt.Sprintf("**Release Notes:**\\n\\n%s\\n\\n", body))
			}
		}

		// Assets (downloads) - show limited info for many releases
		if assets, ok := release["assets"].([]interface{}); ok && len(assets) > 0 {
			if len(releases) > 10 {
				// Just show count for many releases
				markdown.WriteString(fmt.Sprintf("**Downloads:** %d files available\\n\\n", len(assets)))
			} else {
				// Show full details for fewer releases
				markdown.WriteString("**Downloads:**\\n\\n")
				for j, asset := range assets {
					if assetMap, ok := asset.(map[string]interface{}); ok {
						if name, ok := assetMap["name"].(string); ok {
							markdown.WriteString(fmt.Sprintf("%d. `%s`\\n", j+1, name))
						}
						if size, ok := assetMap["size"].(float64); ok {
							markdown.WriteString(fmt.Sprintf("   Size: %.1f MB\\n", size/(1024*1024)))
						}
						if downloadCount, ok := assetMap["download_count"].(float64); ok {
							markdown.WriteString(fmt.Sprintf("   Downloads: %d\\n\\n", int(downloadCount)))
						}
					}
				}
			}
		}

		// Separator
		if i < len(releases)-1 {
			markdown.WriteString("---\\n\\n")
		}
	}

	// Add token estimate for many releases
	if len(releases) > 10 {
		markdown.WriteString(fmt.Sprintf("\\n\\n---\\n\\n*Estimated: ~%d tokens*\\n", len(markdown.String())/4))
	}

	return markdown.String()
}

// ReleaseMetadata contains minimal information for release selection
type ReleaseMetadata struct {
	Index       int    `json:"index"`        // Release number (1 = latest)
	TagName     string `json:"tag_name"`     // v0.9.6
	Name        string `json:"name"`         // Release name
	Date        string `json:"date"`         // 2026-06-01
	Author      string `json:"author"`       // @username
	HasNotes    bool   `json:"has_notes"`    // Whether release has notes
	AssetsCount int    `json:"assets_count"` // Number of download files
	Preview     string `json:"preview"`      // First 100 chars of notes
}

// SmartGitHubResponse represents the intelligent two-phase GitHub response
type SmartGitHubResponse struct {
	Phase        string             `json:"phase"` // "catalog" or "detailed"
	TotalCount   int                `json:"total_count"`
	Releases     []ReleaseMetadata `json:"releases"`
	DetailedData map[string]string `json:"detailed_data,omitempty"` // tag_name -> full markdown
	TokensSaved  int                `json:"tokens_saved"`
}

// githubSmartCatalog creates a release catalog for intelligent LLM selection
// Phase 1: Get all releases with metadata (minimal tokens)
// Phase 2: LLM requests specific releases, load detailed content
func (s *ChromeScraper) githubSmartCatalog(urlStr string) (*SmartGitHubResponse, error) {
	// Extract owner/repo from URL
	var owner, repo string
	if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/releases`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner = matches[1]
		repo = matches[2]
	} else {
		return nil, fmt.Errorf("invalid GitHub URL format")
	}

	// Phase 1: Get ALL releases (metadata only, very compact)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100", owner, repo)

	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	// Add GitHub token if available (increases rate limit from 60/hour to 5000/hour)
	if s.githubCfg.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", s.githubCfg.Token))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API catalog failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API catalog failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read GitHub API catalog response: %w", err)
	}

	var releases []map[string]interface{}
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("failed to parse GitHub API catalog JSON: %w", err)
	}

	// Build compact catalog
	catalog := &SmartGitHubResponse{
		Phase:      "catalog",
		TotalCount: len(releases),
		Releases:   make([]ReleaseMetadata, len(releases)),
	}

	for i, release := range releases {
		metadata := ReleaseMetadata{Index: len(releases) - i}

		if tag, ok := release["tag_name"].(string); ok {
			metadata.TagName = tag
		}
		if name, ok := release["name"].(string); ok {
			metadata.Name = name
		}
		if date, ok := release["published_at"].(string); ok {
			metadata.Date = date[:10]
		}
		if author, ok := release["author"].(map[string]interface{}); ok {
			if login, ok := author["login"].(string); ok {
				metadata.Author = "@" + login
			}
		}
		if body, ok := release["body"].(string); ok {
			metadata.HasNotes = len(body) > 0
			if len(body) > 0 {
				metadata.Preview = truncateString(body, 100)
			}
		}
		if assets, ok := release["assets"].([]interface{}); ok {
			metadata.AssetsCount = len(assets)
		}

		catalog.Releases[i] = metadata
	}

	return catalog, nil
}

// buildCatalogMarkdown creates a compact release catalog for LLM selection
func (s *ChromeScraper) buildCatalogMarkdown(catalog *SmartGitHubResponse) string {
	var md strings.Builder

	md.WriteString(fmt.Sprintf("# GitHub Release Catalog: %d releases available\n\n", catalog.TotalCount))
	md.WriteString("> 🤖 **Smart Mode:** Review this catalog first, then request detailed release notes for specific versions.\n\n")

	for _, release := range catalog.Releases {
		md.WriteString(fmt.Sprintf("## Release %d: %s\n", release.Index, release.Name))
		md.WriteString(fmt.Sprintf("**Tag:** `%s` | **Date:** %s | **Author:** %s\n", release.TagName, release.Date, release.Author))

		if release.AssetsCount > 0 {
			md.WriteString(fmt.Sprintf("**Downloads:** %d files\n", release.AssetsCount))
		}

		if release.HasNotes && release.Preview != "" {
			md.WriteString(fmt.Sprintf("**Preview:** %s...\n", release.Preview))
		} else if !release.HasNotes {
			md.WriteString("**Notes:** (No release notes)\n")
		}

		md.WriteString("\n")
	}

	md.WriteString("\n---\n\n")
	md.WriteString("📋 **Available Commands:**\n")
	md.WriteString("- Request details: \"Tell me about release [index/tag]\"\n")
	md.WriteString("- Compare releases: \"Compare releases [index] and [index]\"\n")
	md.WriteString("- Latest changes: \"What's new in the latest releases?\"\n")

	return md.String()
}

// truncateString safely truncates a string
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// convertGitHubSearchToMarkdown converts GitHub Search API results to Markdown format
// Handles issues and pull requests search results
func (s *ChromeScraper) convertGitHubSearchToMarkdown(items []interface{}) string {
	if len(items) == 0 {
		return "# No results found"
	}

	var markdown strings.Builder

	// Determine item type (issue or pull request)
	itemType := "items"
	if len(items) > 0 {
		if item, ok := items[0].(map[string]interface{}); ok {
			if _, hasPullURL := item["pull_request"]; hasPullURL {
				itemType = "pull requests"
			} else {
				itemType = "issues"
			}
		}
	}

	markdown.WriteString(fmt.Sprintf("# Found %d %s\n\n", len(items), itemType))

	for i, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Item number
		itemNum := i + 1
		markdown.WriteString(fmt.Sprintf("## %s #%d\n", strings.Title(itemType), itemNum))

		// Title
		if title, ok := itemMap["title"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Title:** %s\n\n", title))
		}

		// State (open/closed)
		if state, ok := itemMap["state"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Status:** %s", strings.Title(state)))
			if htmlURL, ok := itemMap["html_url"].(string); ok {
				markdown.WriteString(fmt.Sprintf(" | **URL:** %s", htmlURL))
			}
			markdown.WriteString("\n\n")
		}

		// Number
		if number, ok := itemMap["number"].(float64); ok {
			markdown.WriteString(fmt.Sprintf("**Number:** #%d\n\n", int(number)))
		}

		// Author
		if user, ok := itemMap["user"].(map[string]interface{}); ok {
			if login, ok := user["login"].(string); ok {
				markdown.WriteString(fmt.Sprintf("**Author:** @%s\n\n", login))
			}
		}

		// Labels
		if labels, ok := itemMap["labels"].([]interface{}); ok && len(labels) > 0 {
			markdown.WriteString("**Labels:** ")
			for j, label := range labels {
				if labelMap, ok := label.(map[string]interface{}); ok {
					if name, ok := labelMap["name"].(string); ok {
						if j > 0 {
							markdown.WriteString(", ")
						}
						markdown.WriteString(fmt.Sprintf("`%s`", name))
					}
				}
			}
			markdown.WriteString("\n\n")
		}

		// Created/Updated dates
		if createdAt, ok := itemMap["created_at"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Created:** %s", createdAt[:10]))
			if updatedAt, ok := itemMap["updated_at"].(string); ok {
				markdown.WriteString(fmt.Sprintf(" | **Updated:** %s", updatedAt[:10]))
			}
			markdown.WriteString("\n\n")
		}

		// Body/description (truncated)
		if body, ok := itemMap["body"].(string); ok && body != "" {
			maxBodyLength := 300
			if len(body) > maxBodyLength {
				body = body[:maxBodyLength] + "..."
				markdown.WriteString(fmt.Sprintf("**Description** (truncated):\n\n%s\n\n", body))
			} else {
				markdown.WriteString(fmt.Sprintf("**Description:**\n\n%s\n\n", body))
			}
		}

		// Comments count
		if comments, ok := itemMap["comments"].(float64); ok && comments > 0 {
			markdown.WriteString(fmt.Sprintf("**Comments:** %d\n\n", int(comments)))
		}

		// Separator
		if i < len(items)-1 {
			markdown.WriteString("---\n\n")
		}
	}

	// Add token estimate
	markdown.WriteString(fmt.Sprintf("\n\n*Estimated: ~%d tokens*\n", len(markdown.String())/4))

	return markdown.String()
}

// convertGenericSearchToMarkdown converts generic search results (GitLab, Gitea) to Markdown format
func (s *ChromeScraper) convertGenericSearchToMarkdown(items []interface{}) string {
	if len(items) == 0 {
		return "# No results found"
	}

	var markdown strings.Builder

	markdown.WriteString(fmt.Sprintf("# Found %d items\n\n", len(items)))

	for i, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Title
		if title, ok := itemMap["title"].(string); ok {
			markdown.WriteString(fmt.Sprintf("## %s\n\n", title))
		} else if name, ok := itemMap["name"].(string); ok {
			markdown.WriteString(fmt.Sprintf("## %s\n\n", name))
		}

		// State (open/closed)
		if state, ok := itemMap["state"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Status:** %s\n\n", strings.Title(state)))
		}

		// URL (web_url for GitLab, html_url for others)
		if webURL, ok := itemMap["web_url"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**URL:** %s\n\n", webURL))
		} else if htmlURL, ok := itemMap["html_url"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**URL:** %s\n\n", htmlURL))
		}

		// Author (author.username for GitLab, user.login for others)
		if author, ok := itemMap["author"].(map[string]interface{}); ok {
			if username, ok := author["username"].(string); ok {
				markdown.WriteString(fmt.Sprintf("**Author:** @%s\n\n", username))
			}
		} else if user, ok := itemMap["user"].(map[string]interface{}); ok {
			if login, ok := user["login"].(string); ok {
				markdown.WriteString(fmt.Sprintf("**Author:** @%s\n\n", login))
			}
		}

		// Created/Updated dates
		if createdAt, ok := itemMap["created_at"].(string); ok {
			if len(createdAt) > 10 {
				markdown.WriteString(fmt.Sprintf("**Created:** %s", createdAt[:10]))
			}
		}
		if updatedAt, ok := itemMap["updated_at"].(string); ok {
			if len(updatedAt) > 10 {
				markdown.WriteString(fmt.Sprintf(" | **Updated:** %s", updatedAt[:10]))
			}
		}
		markdown.WriteString("\n\n")

		// Description (truncated)
		if description, ok := itemMap["description"].(string); ok && description != "" {
			maxDescLength := 300
			if len(description) > maxDescLength {
				description = description[:maxDescLength] + "..."
			}
			markdown.WriteString(fmt.Sprintf("**Description** (truncated):\n\n%s\n\n", description))
		}

		// Separator
		if i < len(items)-1 {
			markdown.WriteString("---\n\n")
		}
	}

	markdown.WriteString(fmt.Sprintf("\n\n*Estimated: ~%d tokens*\n", len(markdown.String())/4))
	return markdown.String()
}

// convertGenericReleasesToMarkdown converts generic releases (GitLab, Gitea) to Markdown format
func (s *ChromeScraper) convertGenericReleasesToMarkdown(releases []map[string]interface{}) string {
	if len(releases) == 0 {
		return "# No releases found"
	}

	var markdown strings.Builder
	markdown.WriteString(fmt.Sprintf("# %d Releases\n\n", len(releases)))

	for i, release := range releases {
		// Release name/tag
		var releaseName string
		if name, ok := release["name"].(string); ok && name != "" {
			releaseName = name
		} else if tagName, ok := release["tag_name"].(string); ok {
			releaseName = tagName
		} else if tag, ok := release["tag"].(string); ok {
			releaseName = tag
		}

		markdown.WriteString(fmt.Sprintf("## %s\n\n", releaseName))

		// Tag name
		if tagName, ok := release["tag_name"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Tag:** `%s`\n\n", tagName))
		} else if tag, ok := release["tag"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Tag:** `%s`\n\n", tag))
		}

		// Published date
		if publishedAt, ok := release["published_at"].(string); ok && len(publishedAt) > 10 {
			markdown.WriteString(fmt.Sprintf("**Published:** %s\n\n", publishedAt[:10]))
		} else if createdAt, ok := release["created_at"].(string); ok && len(createdAt) > 10 {
			markdown.WriteString(fmt.Sprintf("**Created:** %s\n\n", createdAt[:10]))
		}

		// Author (for GitLab)
		if author, ok := release["author"].(map[string]interface{}); ok {
			if username, ok := author["username"].(string); ok {
				markdown.WriteString(fmt.Sprintf("**Author:** @%s\n\n", username))
			}
		}

		// Release notes (truncated for efficiency)
		var body string
		if description, ok := release["description"].(string); ok {
			body = description
		} else if notes, ok := release["notes"].(string); ok {
			body = notes
		} else if bodyText, ok := release["body"].(string); ok {
			body = bodyText
		}

		if body != "" {
			maxBodyLength := 500 // Adaptive length
			if len(releases) > 5 {
				maxBodyLength = 200 // Shorter for many releases
			}

			if len(body) > maxBodyLength {
				body = body[:maxBodyLength] + "..."
				markdown.WriteString(fmt.Sprintf("**Release Notes** (truncated):\n\n%s\n\n", body))
			} else {
				markdown.WriteString(fmt.Sprintf("**Release Notes:**\n\n%s\n\n", body))
			}
		}

		// Separator between releases
		if i < len(releases)-1 {
			markdown.WriteString("---\n\n")
		}
	}

	// Add token estimate
	markdown.WriteString(fmt.Sprintf("\n\n*Estimated: ~%d tokens*\n", len(markdown.String())/4))
	return markdown.String()
}

// convertCommitToMarkdown converts a single commit object to Markdown format
func (s *ChromeScraper) convertCommitToMarkdown(commit map[string]interface{}) string {
	var markdown strings.Builder

	// SHA
	if sha, ok := commit["sha"].(string); ok {
		markdown.WriteString(fmt.Sprintf("# Commit %s\n\n", sha[:8]))
	}

	// Commit data (nested object)
	if commitData, ok := commit["commit"].(map[string]interface{}); ok {
		// Author
		if author, ok := commitData["author"].(map[string]interface{}); ok {
			if name, ok := author["name"].(string); ok {
				markdown.WriteString(fmt.Sprintf("**Author:** %s", name))
				if email, ok := author["email"].(string); ok {
					markdown.WriteString(fmt.Sprintf(" <%s>", email))
				}
				if date, ok := author["date"].(string); ok && len(date) > 10 {
					markdown.WriteString(fmt.Sprintf("\n**Date:** %s", date[:10]))
				}
				markdown.WriteString("\n\n")
			}
		}

		// Message
		if message, ok := commitData["message"].(string); ok {
			maxMessageLength := 500
			if len(message) > maxMessageLength {
				message = message[:maxMessageLength] + "..."
			}
			markdown.WriteString(fmt.Sprintf("**Message:**\n\n%s\n\n", message))
		}
	}

	// URL (html_url or web_url)
	if htmlURL, ok := commit["html_url"].(string); ok {
		markdown.WriteString(fmt.Sprintf("**URL:** %s\n\n", htmlURL))
	} else if webURL, ok := commit["web_url"].(string); ok {
		markdown.WriteString(fmt.Sprintf("**URL:** %s\n\n", webURL))
	}

	// Stats (files changed, additions, deletions)
	if stats, ok := commit["stats"].(map[string]interface{}); ok {
		if total, ok := stats["total"].(float64); ok {
			markdown.WriteString(fmt.Sprintf("**Files changed:** %d\n\n", int(total)))
		}
		if additions, ok := stats["additions"].(float64); ok {
			markdown.WriteString(fmt.Sprintf("**Additions:** +%d\n", int(additions)))
		}
		if deletions, ok := stats["deletions"].(float64); ok {
			markdown.WriteString(fmt.Sprintf(" **Deletions:** -%d\n\n", int(deletions)))
		}
	}

	// Token estimate
	markdown.WriteString(fmt.Sprintf("\n*Estimated: ~%d tokens*\n", len(markdown.String())/4))
	return markdown.String()
}

// convertCommitsListToMarkdown converts a list of commits to Markdown format
func (s *ChromeScraper) convertCommitsListToMarkdown(commits []map[string]interface{}) string {
	if len(commits) == 0 {
		return "# No commits found"
	}

	var markdown strings.Builder
	markdown.WriteString(fmt.Sprintf("# %d Commits\n\n", len(commits)))

	for i, commit := range commits {
		// SHA
		var sha string
		if shaValue, ok := commit["sha"].(string); ok {
			sha = shaValue
			markdown.WriteString(fmt.Sprintf("## %s", sha[:8]))
		} else {
			markdown.WriteString(fmt.Sprintf("## Commit #%d", i+1))
		}

		// URL
		if htmlURL, ok := commit["html_url"].(string); ok {
			markdown.WriteString(fmt.Sprintf(" | [View](%s)", htmlURL))
		} else if webURL, ok := commit["web_url"].(string); ok {
			markdown.WriteString(fmt.Sprintf(" | [View](%s)", webURL))
		}
		markdown.WriteString("\n\n")

		// Commit message (from nested commit object)
		if commitData, ok := commit["commit"].(map[string]interface{}); ok {
			// Author
			if author, ok := commitData["author"].(map[string]interface{}); ok {
				if name, ok := author["name"].(string); ok {
					markdown.WriteString(fmt.Sprintf("**Author:** %s", name))
					if date, ok := author["date"].(string); ok && len(date) > 10 {
						markdown.WriteString(fmt.Sprintf(" | %s", date[:10]))
					}
					markdown.WriteString("\n\n")
				}
			}

			// Message (truncated)
			if message, ok := commitData["message"].(string); ok {
				maxMessageLength := 200
				if len(message) > maxMessageLength {
					message = message[:maxMessageLength] + "..."
				}
				markdown.WriteString(fmt.Sprintf("**Message:**\n\n%s\n\n", message))
			}
		}

		// Separator
		if i < len(commits)-1 {
			markdown.WriteString("---\n\n")
		}
	}

	// Token estimate
	markdown.WriteString(fmt.Sprintf("\n\n*Estimated: ~%d tokens*\n", len(markdown.String())/4))
	return markdown.String()
}

// convertRepoToMarkdown converts a repository object to Markdown format
func (s *ChromeScraper) convertRepoToMarkdown(repo map[string]interface{}) string {
	var markdown strings.Builder

	// Repository name and owner
	if name, ok := repo["name"].(string); ok {
		markdown.WriteString(fmt.Sprintf("# %s\n\n", name))
	}

	if owner, ok := repo["owner"].(map[string]interface{}); ok {
		if ownerName, ok := owner["login"].(string); ok {
			markdown.WriteString(fmt.Sprintf("**Owner:** @%s\n\n", ownerName))
		}
	}

	// Description
	if description, ok := repo["description"].(string); ok && description != "" {
		markdown.WriteString(fmt.Sprintf("**Description:**\n\n%s\n\n", description))
	}

	// URL (html_url or web_url)
	if htmlURL, ok := repo["html_url"].(string); ok {
		markdown.WriteString(fmt.Sprintf("**URL:** %s\n\n", htmlURL))
	} else if webURL, ok := repo["web_url"].(string); ok {
		markdown.WriteString(fmt.Sprintf("**URL:** %s\n\n", webURL))
	}

	// Stars, forks, open issues
	if stars, ok := repo["stargazers_count"].(float64); ok {
		markdown.WriteString(fmt.Sprintf("**⭐ Stars:** %d\n", int(stars)))
	}
	if forks, ok := repo["forks_count"].(float64); ok {
		markdown.WriteString(fmt.Sprintf("**🍴 Forks:** %d\n", int(forks)))
	}
	if openIssues, ok := repo["open_issues_count"].(float64); ok {
		markdown.WriteString(fmt.Sprintf("**📋 Open Issues:** %d\n", int(openIssues)))
	}
	if watchers, ok := repo["watchers_count"].(float64); ok {
		markdown.WriteString(fmt.Sprintf("**👀 Watchers:** %d\n", int(watchers)))
	}

	// Language
	if language, ok := repo["language"].(string); ok && language != "" {
		markdown.WriteString(fmt.Sprintf("\n**Language:** %s\n", language))
	}

	// Created/Updated dates
	if createdAt, ok := repo["created_at"].(string); ok && len(createdAt) > 10 {
		markdown.WriteString(fmt.Sprintf("\n**Created:** %s", createdAt[:10]))
	}
	if updatedAt, ok := repo["updated_at"].(string); ok && len(updatedAt) > 10 {
		markdown.WriteString(fmt.Sprintf(" | **Updated:** %s", updatedAt[:10]))
	}

	// License
	if license, ok := repo["license"].(map[string]interface{}); ok {
		if licenseName, ok := license["name"].(string); ok {
			markdown.WriteString(fmt.Sprintf("\n**License:** %s", licenseName))
		} else if licenseKey, ok := license["key"].(string); ok {
			markdown.WriteString(fmt.Sprintf("\n**License:** %s", licenseKey))
		}
	}

	markdown.WriteString("\n\n")

	// Homepage
	if homepage, ok := repo["homepage"].(string); ok && homepage != "" {
		markdown.WriteString(fmt.Sprintf("**Homepage:** %s\n\n", homepage))
	}

	// Is fork/archived
	if isFork, ok := repo["fork"].(bool); ok && isFork {
		markdown.WriteString("**⚠️ This is a fork**\n\n")
	}
	if isArchived, ok := repo["archived"].(bool); ok && isArchived {
		markdown.WriteString("**📦 This repository is archived**\n\n")
	}

	// Token estimate
	markdown.WriteString(fmt.Sprintf("\n*Estimated: ~%d tokens*\n", len(markdown.String())/4))
	return markdown.String()
}

