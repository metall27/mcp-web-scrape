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
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type ScrapeJSTool struct {
	*BaseTool
	cache        *cache.Cache
	browserPool  *browser.Pool
	logger       zerolog.Logger
}

func NewScrapeJSTool(cache *cache.Cache, browserPool *browser.Pool) *ScrapeJSTool {
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
		},
		"required": []string{"url"},
	}

	tool := &ScrapeJSTool{
		cache:       cache,
		browserPool: browserPool,
		logger:      logger.Get(),
	}

	tool.BaseTool = NewBaseTool(
		"scrape_with_js",
		"Get HTML content from URLs using headless Chrome. Works with all websites including GitHub, documentation, blogs, news. Automatically optimizes HTML and takes screenshots for large pages. Auto-indexes to RAG for future semantic search.",
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

	// Check cache first
	if t.cache != nil && t.cache.IsEnabled() {
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
			if screenshotData, ok := cached.Headers["screenshot"]; ok {
				// Decode base64 screenshot from cache
				content = append(content, map[string]interface{}{
					"type":     "image",
					"data":     []byte(screenshotData),
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

	screenshot := false
	if ss, ok := args["screenshot"].(bool); ok {
		screenshot = ss
	}

	screenshotMode := "never"
	if sm, ok := args["screenshot_mode"].(string); ok {
		screenshotMode = sm
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

	// Build tasks
	tasks := []chromedp.Action{
		chromedp.Navigate(urlStr),
	}

	// Wait for specific selector if provided
	if waitFor != "" {
		tasks = append(tasks, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	} else {
		// Wait for page load by default
		tasks = append(tasks, chromedp.WaitReady("body", chromedp.ByQuery))
	}

	// Add extra wait time
	tasks = append(tasks, chromedp.Sleep(time.Duration(waitTime)*time.Millisecond))

	// Get page content
	tasks = append(tasks,
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Location(&finalURL),
	)

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

		// Create HTTP client with redirect following
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

		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP request: %w", err)
		}

		// Set realistic browser headers
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Upgrade-Insecure-Requests", "1")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP fallback also failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP fallback failed with status %d", resp.StatusCode)
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

	result := map[string]interface{}{
		"content": content,
		"_metadata": map[string]interface{}{
			"url":          urlStr,
			"final_url":    finalURL,
			"status_code":  200,
			"content_type": "text/html",
			"size_bytes":   len(html),
			"duration_ms":  duration.Milliseconds(),
			"title":        title,
			"rendering":    "javascript",
		},
	}

	if includeScreenshot {
		result["_metadata"].(map[string]interface{})["screenshot_included"] = true
		result["_metadata"].(map[string]interface{})["screenshot_size"] = len(screenshotData)
	}

	t.logger.Info().
		Str("url", urlStr).
		Str("final_url", finalURL).
		Int("size_bytes", len(html)).
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

	// Store in cache
	if t.cache != nil && t.cache.IsEnabled() {
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

		// Store screenshot in cache if taken
		if includeScreenshot && len(screenshotData) > 0 {
			cachedResp.Headers["screenshot"] = string(screenshotData)
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
	go func() {
		// Get RAG base URL from environment or use default
		ragBaseURL := "https://rag.0x27.ru"
		if envBaseURL := os.Getenv("RAG_BASE_URL"); envBaseURL != "" {
			ragBaseURL = envBaseURL
		}

		// Prepare index request
		indexReq := map[string]interface{}{
			"url": urlStr,
			"processing_mode": "structured",
			"ttl": 7,
		}

		jsonData, _ := json.Marshal(indexReq)

		// Index in background (don't block response)
		resp, err := http.Post(
			ragBaseURL+"/api/v1/index",
			"application/json",
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			t.logger.Warn().Str("url", urlStr).Err(err).Msg("RAG auto-index failed")
		} else {
			defer resp.Body.Close()
			t.logger.Info().Str("url", urlStr).Int("status", resp.StatusCode).Msg("RAG auto-indexed")
		}
	}()

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

	return "scrape_js:" + hex.EncodeToString(hash.Sum(nil))[:16]
}
