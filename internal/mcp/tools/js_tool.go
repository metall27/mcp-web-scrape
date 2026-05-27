package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type ScrapeJSTool struct {
	*BaseTool
	cache        *cache.Cache
	browserPool  *browser.Pool
	ragConfig    config.RAGConfig
	uaRotator    *useragent.Rotator
	proxyRotator *proxy.Rotator
	converter    *converter.Converter
	logger       zerolog.Logger
}

func NewScrapeJSTool(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, uaRotator *useragent.Rotator, proxyRotator *proxy.Rotator) *ScrapeJSTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"format":      "uri",
				"description": "The URL to scrape with JavaScript rendering",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Page load timeout in seconds (default: 60)",
				"default":     60,
			},
			"wait_for": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector to wait for before scraping (optional)",
			},
			"wait_time": map[string]interface{}{
				"type":        "integer",
				"description": "Additional wait time in milliseconds after page load (default: 3000)",
				"default":     3000,
			},
			"screenshot": map[string]interface{}{
				"type":        "boolean",
				"description": "Take a screenshot of the page (base64 encoded)",
				"default":     false,
			},
			"screenshot_mode": map[string]interface{}{
				"type":        "string",
				"description": "When to take screenshot: never, auto (default - if HTML > 50KB), always",
				"enum":        []string{"never", "auto", "always"},
				"default":     "auto",
			},
			"user_agent": map[string]interface{}{
				"type":        "string",
				"description": "Custom user agent string",
			},
			"viewport_width": map[string]interface{}{
				"type":        "integer",
				"description": "Browser viewport width in pixels (default: 1920)",
				"default":     1920,
			},
			"viewport_height": map[string]interface{}{
				"type":        "integer",
				"description": "Browser viewport height in pixels (default: 1080)",
				"default":     1080,
			},
			"block_images": map[string]interface{}{
				"type":        "boolean",
				"description": "Block images from loading (faster scraping)",
				"default":     false,
			},
			"wait_for_network_idle": map[string]interface{}{
				"type":        "boolean",
				"description": "Wait for network idle instead of fixed wait_time (smarter, 30s timeout)",
				"default":     false,
			},
			"output_format": map[string]interface{}{
				"type":        "string",
				"description": "Output format: html (default, optimized HTML) or markdown (better for LLMs)",
				"enum":        []string{"html", "markdown"},
				"default":     "html",
			},
			"stealth_enabled": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable stealth mode (random delays, human-like behavior) to avoid bot detection",
				"default":     false,
			},
			"stealth_scroll": map[string]interface{}{
				"type":        "boolean",
				"description": "Emulate scrolling behavior (many SPAs load content on scroll)",
				"default":     true,
			},
			"stealth_mouse": map[string]interface{}{
				"type":        "boolean",
				"description": "Emulate random mouse movements (advanced anti-bot evasion)",
				"default":     false,
			},
			"actions": map[string]interface{}{
				"type":        "array",
				"description": "Interactive actions to execute after page load (click, type, scroll_to, wait_for, etc.). Actions are not cached.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"click", "type", "submit", "scroll_to", "wait_for", "wait_for_text", "hover", "select_option", "execute_js", "upload_file"},
							"description": "Action type to perform",
						},
						"selector": map[string]interface{}{
							"type":        "string",
							"description": "CSS selector for the element (required for most actions)",
						},
						"text": map[string]interface{}{
							"type":        "string",
							"description": "Text to type or JavaScript code to execute (for 'type' and 'execute_js' actions)",
						},
						"value": map[string]interface{}{
							"type":        "string",
							"description": "Value to select in dropdown (for 'select_option' action)",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Timeout in milliseconds for wait actions (default: 30000)",
						},
						"retries": map[string]interface{}{
							"type":        "integer",
							"description": "Number of retries on failure (default: 3)",
						},
					},
					"required": []string{"type"},
				},
			},
		},
		"required": []string{"url"},
	}

	tool := &ScrapeJSTool{
		cache:        cache,
		browserPool:  browserPool,
		ragConfig:    ragConfig,
		uaRotator:    uaRotator,
		proxyRotator: proxyRotator,
		converter:    converter.New(),
		logger:       logger.Get(),
	}

	tool.BaseTool = NewBaseTool(
		"scrape_with_js",
		"Get HTML content from URLs using headless Chrome. Works with all websites including GitHub, documentation, blogs, news. Automatically optimizes HTML and takes screenshots for large pages. Auto-indexes to RAG for future semantic search. Supports smart network idle waiting (wait_for_network_idle=true) for optimal performance on SPA sites. Interactive actions support (click, type, scroll, wait_for) for login-protected content and dynamic elements.",
		schema,
		tool.Execute,
	)

	return tool
}

func (t *ScrapeJSTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Extract URL
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, fmt.Errorf("url is required and must be a string")
	}

	// Validate URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("only http and https schemes are supported")
	}

	// Parse interactive actions if provided (move this before cache check)
	var interactiveActions []browser.Action
	hasActions := false
	if actionsData, ok := args["actions"].([]interface{}); ok && len(actionsData) > 0 {
		parsedActions, err := browser.ParseActions(actionsData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse actions: %w", err)
		}
		interactiveActions = parsedActions
		hasActions = true
		t.logger.Info().
			Int("actions_count", len(interactiveActions)).
			Msg("Interactive actions detected, cache will be bypassed")
	}

	// Check cache first (but bypass if actions are present)
	if t.cache != nil && t.cache.IsEnabled() && !hasActions {
		cacheKey := t.getCacheKey(urlStr, args)
		if cached, found := t.cache.Get(ctx, cacheKey); found {
			t.logger.Info().
				Str("url", urlStr).
				Str("cache_key", cacheKey).
				Msg("Cache hit")

			content := []map[string]interface{}{
				{
					"type": "text",
					"text": string(cached.Data),
				},
			}

			result := map[string]interface{}{
				"content": content,
				"_metadata": map[string]interface{}{
					"url":          urlStr,
					"status_code":  200,
					"content_type": cached.Headers["content_type"],
					"size_bytes":   len(cached.Data),
					"cached":       true,
					"duration_ms":  0,
					"rendering":    "javascript",
				},
			}

			// Add title if available
			if title, ok := cached.Headers["title"]; ok {
				result["_metadata"].(map[string]interface{})["title"] = title
			}

			// Add screenshot if available in cache
			if len(cached.Screenshot) > 0 {
				content = append(content, map[string]interface{}{
					"type":     "image",
					"data":     cached.Screenshot,
					"mimeType": "image/png",
				})
				result["_metadata"].(map[string]interface{})["screenshot_included"] = true
			}

			return result, nil
		}
	}

	// Extract options
	timeout := 60
	if timeoutSec, ok := args["timeout"].(float64); ok {
		timeout = int(timeoutSec)
	}

	waitFor := ""
	if wf, ok := args["wait_for"].(string); ok {
		waitFor = wf
	}

	waitTime := 3000
	if wt, ok := args["wait_time"].(float64); ok {
		waitTime = int(wt)
	}

	waitForNetworkIdle := false
	if wfi, ok := args["wait_for_network_idle"].(bool); ok {
		waitForNetworkIdle = wfi
	}

	screenshot := false
	if ss, ok := args["screenshot"].(bool); ok {
		screenshot = ss
	}

	screenshotMode := "never"
	if sm, ok := args["screenshot_mode"].(string); ok {
		screenshotMode = sm
	}

	outputFormat := "html"
	if of, ok := args["output_format"].(string); ok {
		outputFormat = of
	}

	// Stealth settings
	stealthEnabled := false
	if se, ok := args["stealth_enabled"].(bool); ok {
		stealthEnabled = se
	}

	stealthScroll := true
	if ss, ok := args["stealth_scroll"].(bool); ok {
		stealthScroll = ss
	}

	stealthMouse := false
	if sm, ok := args["stealth_mouse"].(bool); ok {
		stealthMouse = sm
	}

	// Determine if we should take screenshot
	shouldScreenshot := screenshot
	if screenshotMode == "always" {
		shouldScreenshot = true
	}

	t.logger.Info().
		Str("url", urlStr).
		Int("timeout", timeout).
		Str("wait_for", waitFor).
		Int("wait_time", waitTime).
		Bool("wait_for_network_idle", waitForNetworkIdle).
		Bool("stealth_enabled", stealthEnabled).
		Bool("stealth_scroll", stealthScroll).
		Bool("stealth_mouse", stealthMouse).
		Str("output_format", outputFormat).
		Bool("screenshot", shouldScreenshot).
		Str("screenshot_mode", screenshotMode).
		Msg("Starting JavaScript-rendered scrape")

	// Create context with timeout
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer timeoutCancel()

	// Get browser context from pool
	browserCtx, browserCancel, err := t.browserPool.GetContext(timeoutCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get browser context: %w", err)
	}
	defer browserCancel()

	// Result variables
	var html string
	var screenshotData []byte
	var title string
	var finalURL string

	startTime := time.Now()

	// Get random User-Agent for this request
	userAgent := t.uaRotator.Get()
	t.logger.Debug().
		Str("user_agent", userAgent).
		Str("url", urlStr).
		Msg("Using random User-Agent")

	// Setup stealth actions if enabled
	var stealth *browser.StealthActions
	if stealthEnabled {
		stealth = browser.NewStealthActions(browser.StealthConfig{
			RandomDelay:     true,
			MinDelay:        100 * time.Millisecond,
			MaxDelay:        500 * time.Millisecond,
			EmulateScroll:   stealthScroll,
			ScrollSteps:     3,
			MouseMovement:   stealthMouse,
			RandomViewport:  false,
		})
		t.logger.Info().
			Bool("stealth_enabled", true).
			Bool("stealth_scroll", stealthScroll).
			Bool("stealth_mouse", stealthMouse).
			Msg("Stealth mode enabled")
	}

	// Get proxy if enabled
	var selectedProxy *proxy.Proxy
	if t.proxyRotator.IsEnabled() {
		var err error
		selectedProxy, err = t.proxyRotator.GetNext()
		if err != nil {
			t.logger.Warn().Err(err).Msg("Failed to get proxy, continuing without proxy")
		} else if selectedProxy != nil {
			t.logger.Info().
				Str("proxy", selectedProxy.URL).
				Msg("Using proxy for request")
		}
	}

	// Build tasks
	tasks := []chromedp.Action{
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Set User-Agent via CDP before navigation
			var result interface{}
			return chromedp.Evaluate(`Object.defineProperty(navigator, 'userAgent', {get: function() {return "`+userAgent+`"}})`, &result).Do(ctx)
		}),
	}

	// Apply stealth to navigation if enabled
	navigateAction := chromedp.Navigate(urlStr)
	if stealth != nil {
		navigateAction = stealth.ApplyStealth(navigateAction)
	}
	tasks = append(tasks, navigateAction)

	// Wait for specific selector if provided
	if waitFor != "" {
		waitAction := chromedp.WaitVisible(waitFor, chromedp.ByQuery)
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
	if waitForNetworkIdle {
		// Smart wait: wait for network idle with timeout
		waitAction := browser.NetworkIdleAdvanced(browser.NetworkIdleOption{
			Timeout:    30 * time.Second, // Maximum wait time
			MinWait:    time.Duration(waitTime) * time.Millisecond, // Minimum wait (respect user's wait_time)
			CheckCount: 3, // Need 3 consecutive checks to confirm idle
		})
		tasks = append(tasks, waitAction)
		t.logger.Info().
			Bool("network_idle", true).
			Int("min_wait_ms", waitTime).
			Msg("Using network idle wait strategy")
	} else {
		// Simple fixed delay
		tasks = append(tasks, chromedp.Sleep(time.Duration(waitTime)*time.Millisecond))
		t.logger.Debug().
			Int("wait_ms", waitTime).
			Msg("Using fixed delay wait strategy")
	}

	// Add stealth scroll after page load if enabled
	if stealth != nil && stealthScroll {
		tasks = append(tasks, stealth.EmulateScroll())
		t.logger.Debug().
			Msg("Added stealth scroll emulation")
	}

	// Get page content
	tasks = append(tasks,
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Location(&finalURL),
	)

	// Add stealth mouse movements before screenshot if enabled
	if stealth != nil && stealthMouse {
		tasks = append(tasks, stealth.EmulateMouseMovement())
		t.logger.Debug().
			Msg("Added stealth mouse movement emulation")
	}

	// Execute interactive actions if provided (before getting content)
	if hasActions {
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			t.logger.Info().
				Int("actions_count", len(interactiveActions)).
				Msg("Executing interactive actions")

			// Create action executor with stealth support
			actionExecutor := browser.NewActionExecutor(t.logger, stealth)

			// Execute all actions
			if err := actionExecutor.ExecuteActions(ctx, interactiveActions); err != nil {
				return fmt.Errorf("failed to execute actions: %w", err)
			}

			t.logger.Info().
				Msg("All interactive actions completed successfully")

			return nil
		}))
	}

	// Take screenshot if requested (always take in auto mode, decide later whether to include)
	if shouldScreenshot || screenshotMode == "auto" {
		tasks = append(tasks, chromedp.FullScreenshot(&screenshotData, 90))
	}

	// Run tasks
	if err := chromedp.Run(browserCtx, tasks...); err != nil {
		// Chrome failed, try fallback to HTTP scraping
		t.logger.Warn().
			Str("url", urlStr).
			Err(err).
			Msg("Chrome scraping failed, attempting HTTP fallback")

		// Create HTTP client with redirect following and proxy
		client := &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Allow up to 10 redirects
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}

		// Add proxy to HTTP client if enabled
		if t.proxyRotator.IsEnabled() {
			client.Transport = &http.Transport{
				Proxy: t.proxyRotator.GetProxyFunc(),
			}
			t.logger.Info().
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
			if t.proxyRotator.IsEnabled() {
				t.proxyRotator.MarkFailure(err)
			}
			return nil, fmt.Errorf("HTTP fallback also failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			if t.proxyRotator.IsEnabled() {
				t.proxyRotator.MarkFailure(fmt.Errorf("HTTP %d", resp.StatusCode))
			}
			return nil, fmt.Errorf("HTTP fallback failed with status %d", resp.StatusCode)
		}

		// Mark proxy as successful
		if t.proxyRotator.IsEnabled() {
			t.proxyRotator.MarkSuccess()
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read HTTP response: %w", err)
		}

		// Check if response is too small (likely an error or redirect)
		if len(body) < 100 {
			t.logger.Warn().
				Int("size", len(body)).
				Msg("HTTP fallback returned very small response, might be an error page")
		}

		// Use HTTP response
		html = string(body)
		finalURL = resp.Request.URL.String()

		t.logger.Info().
			Str("method", "HTTP fallback").
			Int("size", len(html)).
			Str("final_url", finalURL).
			Msg("Successfully scraped with HTTP fallback")
	}

	duration := time.Since(startTime)

	// Check HTML size BEFORE optimization for screenshot decision
	originalHTMLSize := len(html)

	// Optimize HTML to reduce size
	if strings.Contains(urlStr, "github.com") {
		html = string(OptimizeGitHubHTML([]byte(html)))
	} else {
		html = string(OptimizeHTML([]byte(html)))
	}

	optimizedSize := len(html)
	t.logger.Info().
		Int("original_size", originalHTMLSize).
		Int("optimized_size", optimizedSize).
		Int("reduction", originalHTMLSize-optimizedSize).
		Float64("reduction_percent", float64(originalHTMLSize-optimizedSize)/float64(originalHTMLSize)*100).
		Msg("HTML optimized for inference")

	// Convert to Markdown if requested
	var converted bool
	var outputConverterStats *converter.ConversionStats
	if outputFormat == "markdown" {
		var convertedHTML string
		var err error

		convertedHTML, outputConverterStats, err = t.converter.ConvertWithStats(html, converter.FormatMarkdown)
		if err != nil {
			t.logger.Warn().
				Err(err).
				Str("output_format", outputFormat).
				Msg("Markdown conversion failed, falling back to HTML")
			outputFormat = "html" // Fallback to HTML
		} else {
			html = convertedHTML
			converted = true
			t.logger.Info().
				Int("html_size", optimizedSize).
				Int("markdown_size", outputConverterStats.FinalSize).
				Int("reduction", outputConverterStats.Reduction).
				Float64("reduction_percent", outputConverterStats.ReductionPct).
				Msg("Converted HTML to Markdown")
		}
	}

	// Decide whether to include screenshot based on mode
	includeScreenshot := shouldScreenshot
	if screenshotMode == "auto" && len(screenshotData) > 0 {
		// Include screenshot if ORIGINAL HTML is large (> 50KB)
		// This ensures we don't skip screenshot due to successful optimization
		includeScreenshot = originalHTMLSize > 50*1024
		t.logger.Info().
			Int("original_html_size", originalHTMLSize).
			Int("threshold", 50*1024).
			Bool("include_screenshot", includeScreenshot).
			Msg("Screenshot decision based on original HTML size")
	}

	// Build result in MCP format
	content := []map[string]interface{}{
		{
			"type": "text",
			"text": html,
		},
	}

	// Add screenshot as image content if applicable
	if includeScreenshot && len(screenshotData) > 0 {
		content = append(content, map[string]interface{}{
			"type": "image",
			"data": screenshotData,
			"mimeType": "image/png",
		})
	}

	// Determine content type based on format
	contentType := "text/html"
	if outputFormat == "markdown" {
		contentType = "text/markdown"
	}

	result := map[string]interface{}{
		"content": content,
		"_metadata": map[string]interface{}{
			"url":          urlStr,
			"final_url":    finalURL,
			"status_code":  200,
			"content_type": contentType,
			"size_bytes":   len(html),
			"duration_ms":  duration.Milliseconds(),
			"title":        title,
			"rendering":    "javascript",
			"format":       outputFormat,
		},
	}

	// Add action metadata if interactive actions were executed
	if hasActions {
		result["_metadata"].(map[string]interface{})["interactive_actions"] = map[string]interface{}{
			"count":        len(interactiveActions),
			"action_types": getActionTypes(interactiveActions),
			"cached":       false, // Actions are never cached
		}
		t.logger.Info().
			Int("actions_count", len(interactiveActions)).
			Msg("Interactive actions metadata added to result")
	}

	// Add conversion stats if Markdown was generated
	if converted && outputConverterStats != nil {
		result["_metadata"].(map[string]interface{})["conversion_stats"] = map[string]interface{}{
			"original_size":     outputConverterStats.OriginalSize,
			"final_size":        outputConverterStats.FinalSize,
			"reduction":         outputConverterStats.Reduction,
			"reduction_percent": outputConverterStats.ReductionPct,
			"format":            string(outputConverterStats.Format),
		}
	}

	if includeScreenshot {
		result["_metadata"].(map[string]interface{})["screenshot_included"] = true
		result["_metadata"].(map[string]interface{})["screenshot_size"] = len(screenshotData)
	}

	t.logger.Info().
		Str("url", urlStr).
		Str("final_url", finalURL).
		Int("size_bytes", len(html)).
		Str("format", outputFormat).
		Int64("duration_ms", duration.Milliseconds()).
		Bool("screenshot_included", includeScreenshot).
		Msg("Successfully scraped URL with JavaScript")

	t.logger.Info().
		Str("url", urlStr).
		Str("final_url", finalURL).
		Int("size_bytes", len(html)).
		Int64("duration_ms", duration.Milliseconds()).
		Bool("screenshot", screenshot).
		Msg("Successfully scraped URL with JavaScript")

	// Store in cache (only if no interactive actions)
	if t.cache != nil && t.cache.IsEnabled() && !hasActions {
		cacheKey := t.getCacheKey(urlStr, args)
		ttl := t.cache.GetTTLForContentType("text/html")

		cachedResp := &cache.CachedResponse{
			Data:      []byte(html),
			Timestamp: time.Now(),
			Headers: map[string]string{
				"content_type": "text/html",
				"title":        title,
				"final_url":    finalURL,
			},
		}

		// Store screenshot in cache if taken (use separate field for binary data)
		if includeScreenshot && len(screenshotData) > 0 {
			cachedResp.Screenshot = screenshotData
			cachedResp.Headers["screenshot_size"] = fmt.Sprintf("%d", len(screenshotData))
		}

		if err := t.cache.Set(ctx, cacheKey, cachedResp, ttl); err != nil {
			t.logger.Error().
				Str("cache_key", cacheKey).
				Err(err).
				Msg("Failed to store in cache")
		} else {
			t.logger.Info().
				Str("cache_key", cacheKey).
				Dur("ttl", ttl).
				Msg("Stored in cache")
		}
	}

	// Auto-index in RAG (background, non-blocking)
	if t.ragConfig.Enabled {
		go func() {
			// Prepare index request
			indexReq := map[string]interface{}{
				"url": urlStr,
				"processing_mode": "structured",
				"ttl": 7,
			}

			jsonData, _ := json.Marshal(indexReq)
			indexURL := t.ragConfig.BaseURL + "/api/v1/index"

			// Retry logic
			var lastErr error
			for attempt := 0; attempt <= t.ragConfig.MaxRetries; attempt++ {
				if attempt > 0 {
					// Wait before retry
					time.Sleep(time.Duration(t.ragConfig.RetryDelay) * time.Second)
					t.logger.Debug().
						Str("url", urlStr).
						Int("attempt", attempt).
						Msg("Retrying RAG index")
				}

				// Index in background (don't block response)
				resp, err := http.Post(
					indexURL,
					"application/json",
					bytes.NewBuffer(jsonData),
				)
				if err != nil {
					lastErr = err
					t.logger.Warn().
						Str("url", urlStr).
						Int("attempt", attempt).
						Err(err).
						Msg("RAG auto-index attempt failed")
					continue
				}

				// Success
				resp.Body.Close()
				t.logger.Info().
					Str("url", urlStr).
					Int("status", resp.StatusCode).
					Int("attempt", attempt).
					Msg("RAG auto-indexed")
				return
			}

			// All retries failed
			t.logger.Error().
				Str("url", urlStr).
				Err(lastErr).
				Int("max_retries", t.ragConfig.MaxRetries).
				Msg("RAG auto-index failed after all retries")
		}()
	}

	return result, nil
}

// getCacheKey generates a cache key based on URL and parameters
func (t *ScrapeJSTool) getCacheKey(url string, args map[string]interface{}) string {
	hash := sha256.New()
	hash.Write([]byte(url))

	// Include relevant parameters in hash for JS scraping
	// Different parameters = different result
	keys := []string{"wait_for", "wait_time", "viewport_width", "viewport_height", "block_images"}
	for _, key := range keys {
		if val, ok := args[key]; ok {
			hash.Write([]byte(fmt.Sprintf("%s:%v", key, val)))
		}
	}

	// Include user agent if custom
	if ua, ok := args["user_agent"].(string); ok && ua != "" {
		hash.Write([]byte("user_agent:" + ua))
	}

	// Include actions hash (if any) - actions with same parameters should have same cache key
	if actionsData, ok := args["actions"].([]interface{}); ok && len(actionsData) > 0 {
		// Create a deterministic hash of actions
		actionsJSON, _ := json.Marshal(actionsData)
		hash.Write([]byte("actions:" + string(actionsJSON)))
	}

	return "scrape_js:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// getActionTypes возвращает список типов действий для метаданных
func getActionTypes(actions []browser.Action) []string {
	types := make([]string, len(actions))
	for i, action := range actions {
		types[i] = action.Type
	}
	return types
}
