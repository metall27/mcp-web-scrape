package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

type ScrapeJSTool struct {
	*BaseTool
	scraper    *ChromeScraper
	cache      *cache.Cache
	ragConfig  config.RAGConfig
	logger     zerolog.Logger
}

func NewScrapeJSTool(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, browserCfg config.BrowserConfig, uaRotator *useragent.Rotator, proxyRotator *proxy.Rotator) *ScrapeJSTool {
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
				"description": "Output format: markdown (default, 75% smaller, better for LLMs) or html (raw HTML)",
				"enum":        []string{"html", "markdown"},
				"default":     "markdown",
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

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &ScrapeJSTool{
			cache:     cache,
			ragConfig: ragConfig,
			logger:    logger.Get(),
		}
		tool.scraper = NewChromeScraper(cache, browserPool, ragConfig, browserCfg, uaRotator, proxyRotator)
		return tool.Execute(ctx, args)
	}

	tool := &ScrapeJSTool{
		cache:     cache,
		ragConfig: ragConfig,
		logger:    logger.Get(),
	}
	tool.scraper = NewChromeScraper(cache, browserPool, ragConfig, browserCfg, uaRotator, proxyRotator)

	tool.BaseTool = NewBaseTool(
		"scrape_with_js",
		"Get content from URLs using headless Chrome. Works with all websites including GitHub, documentation, blogs, news. Returns Markdown by default (75% smaller, optimized for LLMs). Automatically optimizes content and takes screenshots for large pages. Auto-indexes to RAG for future semantic search. Supports smart network idle waiting (wait_for_network_idle=true) for optimal performance on SPA sites. Interactive actions support (click, type, scroll, wait_for) for login-protected content and dynamic elements.",
		schema,
		handler,
	)

	return tool
}

func (t *ScrapeJSTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Extract URL
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, fmt.Errorf("url is required and must be a string")
	}

	// Parse interactive actions if provided
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
			Msg("Interactive actions detected")
	}

	// Build options from args
	opts := t.buildOptions(args, interactiveActions)

	// Log request
	t.logger.Info().
		Str("url", urlStr).
		Int("timeout", int(opts.Timeout.Seconds())).
		Str("wait_for", opts.WaitForSelector).
		Int("wait_time_ms", int(opts.WaitForDuration.Milliseconds())).
		Bool("wait_for_network_idle", opts.WaitForNetworkIdle).
		Bool("stealth_enabled", opts.StealthEnabled).
		Bool("stealth_scroll", opts.StealthScroll).
		Bool("stealth_mouse", opts.StealthMouse).
		Str("output_format", opts.OutputFormat).
		Bool("screenshot", opts.Screenshot).
		Str("screenshot_mode", opts.ScreenshotMode).
		Msg("Starting JavaScript-rendered scrape")

	// Validate scraper is initialized
	if t.scraper == nil {
		return nil, fmt.Errorf("Chrome scraper is not initialized - this is a configuration error")
	}

	// Execute scrape using ChromeScraper
	result, err := t.scraper.Scrape(ctx, urlStr, opts)
	if err != nil {
		return nil, err
	}

	// Build result in MCP format
	content := []map[string]interface{}{
		{
			"type": "text",
			"text": result.HTML,
		},
	}

	// Add screenshot as image content if applicable
	includeScreenshot := t.shouldIncludeScreenshot(opts.Screenshot, opts.ScreenshotMode, result.HTML)
	if includeScreenshot && len(result.Screenshot) > 0 {
		content = append(content, map[string]interface{}{
			"type":     "image",
			"data":     result.Screenshot,
			"mimeType": "image/png",
		})
	}

	// Determine content type based on format
	contentType := "text/html"
	if result.Format == "markdown" {
		contentType = "text/markdown"
	}

	metadata := map[string]interface{}{
		"url":           result.URL,
		"final_url":     result.FinalURL,
		"status_code":   result.StatusCode,
		"content_type":  contentType,
		"size_bytes":    result.SizeBytes,
		"duration_ms":   result.Duration.Milliseconds(),
		"title":         result.Title,
		"rendering":     "javascript",
		"format":        result.Format,
		"method":        result.Method,
	}

	// Add action metadata if interactive actions were executed
	if hasActions && result.ActionsMetadata != nil {
		metadata["interactive_actions"] = map[string]interface{}{
			"count":        result.ActionsMetadata.Count,
			"action_types": result.ActionsMetadata.Types,
			"cached":       false,
		}
		t.logger.Info().
			Int("actions_count", result.ActionsMetadata.Count).
			Msg("Interactive actions metadata added to result")
	}

	if result.FromCache {
		metadata["cached"] = true
		metadata["duration_ms"] = 0
	}

	if includeScreenshot && len(result.Screenshot) > 0 {
		metadata["screenshot_included"] = true
		metadata["screenshot_size"] = len(result.Screenshot)
	}

	// Auto-index in RAG (background, non-blocking)
	if t.ragConfig.Enabled {
		go t.indexToRAG(urlStr, result)
	}

	t.logger.Info().
		Str("url", urlStr).
		Str("final_url", result.FinalURL).
		Int("size_bytes", result.SizeBytes).
		Int64("duration_ms", result.Duration.Milliseconds()).
		Bool("screenshot_included", includeScreenshot && len(result.Screenshot) > 0).
		Msg("Successfully scraped URL with JavaScript")

	return map[string]interface{}{
		"content":   content,
		"_metadata": metadata,
	}, nil
}

// buildOptions converts args to Options
func (t *ScrapeJSTool) buildOptions(args map[string]interface{}, actions []browser.Action) Options {
	// Extract timeout
	timeout := 60
	if timeoutSec, ok := args["timeout"].(float64); ok {
		timeout = int(timeoutSec)
	}

	// Extract wait_for
	waitFor := ""
	if wf, ok := args["wait_for"].(string); ok {
		waitFor = wf
	}

	// Extract wait_time
	waitTime := 3000
	if wt, ok := args["wait_time"].(float64); ok {
		waitTime = int(wt)
	}

	// Extract wait_for_network_idle
	waitForNetworkIdle := false
	if wfi, ok := args["wait_for_network_idle"].(bool); ok {
		waitForNetworkIdle = wfi
	}

	// Extract screenshot
	screenshot := false
	if ss, ok := args["screenshot"].(bool); ok {
		screenshot = ss
	}

	// Extract screenshot_mode
	screenshotMode := "auto"
	if sm, ok := args["screenshot_mode"].(string); ok {
		screenshotMode = sm
	}

	// Extract output_format
	outputFormat := "markdown"  // Default to markdown for 75% token savings
	if of, ok := args["output_format"].(string); ok {
		outputFormat = of
	}

	// Extract user_agent
	userAgent := ""
	if ua, ok := args["user_agent"].(string); ok {
		userAgent = ua
	}

	// Extract viewport
	viewportWidth := 1920
	if vw, ok := args["viewport_width"].(float64); ok {
		viewportWidth = int(vw)
	}

	viewportHeight := 1080
	if vh, ok := args["viewport_height"].(float64); ok {
		viewportHeight = int(vh)
	}

	// Extract block_images
	blockImages := false
	if bi, ok := args["block_images"].(bool); ok {
		blockImages = bi
	}

	// Extract stealth settings
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

	return Options{
		Timeout:            time.Duration(timeout) * time.Second,
		WaitForSelector:    waitFor,
		WaitForDuration:    time.Duration(waitTime) * time.Millisecond,
		WaitForNetworkIdle: waitForNetworkIdle,
		Screenshot:         screenshot,
		ScreenshotMode:     screenshotMode,
		UserAgent:          userAgent,
		ViewportWidth:      viewportWidth,
		ViewportHeight:     viewportHeight,
		BlockImages:       blockImages,
		OutputFormat:       outputFormat,
		StealthEnabled:     stealthEnabled,
		StealthScroll:      stealthScroll,
		StealthMouse:       stealthMouse,
		Actions:            actions,
	}
}

// shouldIncludeScreenshot determines whether to include screenshot based on mode and HTML size
func (t *ScrapeJSTool) shouldIncludeScreenshot(screenshot bool, screenshotMode string, html string) bool {
	if screenshot {
		return true
	}

	if screenshotMode == "always" {
		return true
	}

	if screenshotMode == "auto" {
		// Include screenshot if HTML is large (> 50KB)
		return len(html) > 50*1024
	}

	return false
}

// indexToRAG indexes the scraped content to RAG in the background
func (t *ScrapeJSTool) indexToRAG(url string, result *Result) {
	// Prepare index request
	indexReq := map[string]interface{}{
		"url":             url,
		"processing_mode": "structured",
		"ttl":             7,
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
				Str("url", url).
				Int("attempt", attempt).
				Msg("Retrying RAG index")
		}

		// Index in background (don't block response)
		resp, err := http.Post(
			indexURL,
			"application/json",
			strings.NewReader(string(jsonData)),
		)
		if err != nil {
			lastErr = err
			t.logger.Warn().
				Str("url", url).
				Int("attempt", attempt).
				Err(err).
				Msg("RAG auto-index attempt failed")
			continue
		}

		// Success
		resp.Body.Close()
		t.logger.Info().
			Str("url", url).
			Int("status", resp.StatusCode).
			Int("attempt", attempt).
			Msg("RAG auto-indexed")
		return
	}

	// All retries failed
	t.logger.Error().
		Str("url", url).
		Err(lastErr).
		Int("max_retries", t.ragConfig.MaxRetries).
		Msg("RAG auto-index failed after all retries")
}