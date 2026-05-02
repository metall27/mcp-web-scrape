package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type ProcessingMode string

const (
	ProcessingModeAuto      ProcessingMode = "auto"      // AI decides based on content
	ProcessingModeFast      ProcessingMode = "fast"      // Text only, no screenshots
	ProcessingModeBalanced ProcessingMode = "balanced"  // Text + screenshots if >50KB
	ProcessingModeThorough  ProcessingMode = "thorough"  // Always text + screenshot
	ProcessingModePreview   ProcessingMode = "preview"   // Structure only (headers, sections)
)

type ScrapeJSTool struct {
	*BaseTool
	logger zerolog.Logger
}

func NewScrapeJSTool() *ScrapeJSTool {
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
			"processing_mode": map[string]interface{}{
				"type":        "string",
				"description": "Processing strategy: auto (smart routing), fast (text only), balanced (text+screenshots>50KB), thorough (always screenshot), preview (structure only)",
				"enum":        []string{"auto", "fast", "balanced", "thorough", "preview"},
				"default":     "auto",
			},
			"extract_sections": map[string]interface{}{
				"type":        "array",
				"description": "List of section titles/IDs to extract (e.g., ['Introduction', 'Methods']). Only extracts matching sections to save tokens. Empty = extract all.",
				"items": map[string]interface{}{
					"type": "string",
				},
				"default":     []string{},
			},
			"max_size": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum HTML size to return in bytes (0 = no limit). If exceeded, returns preview + truncation notice.",
				"default":     0,
			},
		},
		"required": []string{"url"},
	}

	tool := &ScrapeJSTool{
		logger: logger.Get(),
	}

	tool.BaseTool = NewBaseTool(
		"scrape_with_js",
		"Universal web scraping tool - works with ALL websites including static pages, blogs, news, GitHub, dashboards, SPAs. Uses headless Chrome for JavaScript rendering. Automatically optimizes HTML and takes screenshots for large pages (>50KB) to reduce token usage. Processing modes: auto (smart routing), fast (text only), balanced (text+screenshots>50KB), thorough (always screenshot), preview (structure only). Token-saving features: extract_sections (get only specific sections), max_size (limit response size). This is the ONLY scraping tool needed - it handles both static and dynamic content efficiently.",
		schema,
		tool.Execute,
	)

	return tool
}

// getAutoProcessingMode decides the best processing mode based on URL and content size
func getAutoProcessingMode(url string, htmlSize int) ProcessingMode {
	// Very small pages - fast mode (text only)
	if htmlSize < 20*1024 {
		return ProcessingModeFast
	}

	// Very large pages - preview mode for analysis
	if htmlSize > 100*1024 {
		// Documentation and technical docs - preview first
		if strings.Contains(url, "docs.") ||
		   strings.Contains(url, "documentation") ||
		   strings.Contains(url, "/docs/") {
			return ProcessingModePreview
		}
		// News sites - balanced (they optimize well)
		if strings.Contains(url, "news.") {
			return ProcessingModeBalanced
		}
		// Default for very large pages
		return ProcessingModeBalanced
	}

	// Medium pages (20-100KB)
	// GitHub - optimizes very well
	if strings.Contains(url, "github.com") {
		return ProcessingModeBalanced
	}

	// News/blogs - fast mode (simple text)
	if strings.Contains(url, "news.") ||
	   strings.Contains(url, "blog.") {
		return ProcessingModeFast
	}

	// API documentation
	if strings.Contains(url, "api.") ||
	   strings.Contains(url, "/api/") {
		return ProcessingModeBalanced
	}

	// Default for medium pages
	return ProcessingModeBalanced
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

	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	if ua, ok := args["user_agent"].(string); ok && ua != "" {
		userAgent = ua
	}

	viewportWidth := 1920
	if vw, ok := args["viewport_width"].(float64); ok {
		viewportWidth = int(vw)
	}

	viewportHeight := 1080
	if vh, ok := args["viewport_height"].(float64); ok {
		viewportHeight = int(vh)
	}

	blockImages := false
	if bi, ok := args["block_images"].(bool); ok {
		blockImages = bi
	}

	processingMode := ProcessingModeAuto
	if pm, ok := args["processing_mode"].(string); ok {
		processingMode = ProcessingMode(pm)
	}

	// Extract sections parameter
	var extractSections []string
	if es, ok := args["extract_sections"].([]interface{}); ok {
		for _, section := range es {
			if sectionStr, ok := section.(string); ok {
				extractSections = append(extractSections, sectionStr)
			}
		}
	}

	// Max size parameter
	maxSize := 0
	if ms, ok := args["max_size"].(float64); ok {
		maxSize = int(ms)
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
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Create chromedp context with options
	allocOpts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Headless,
		chromedp.UserAgent(userAgent),
		chromedp.WindowSize(viewportWidth, viewportHeight),
		// Stealth mode to avoid bot detection
		chromedp.Flag("exclude-switches", "enable-automation"),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
	}

	// Add image blocking if requested
	if blockImages {
		allocOpts = append(allocOpts,
			chromedp.Flag("blink-settings", "imagesEnabled=false"),
		)
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancel()

	ctx, cancel = chromedp.NewContext(allocCtx)
	defer cancel()

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
	if err := chromedp.Run(ctx, tasks...); err != nil {
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

	// Determine actual processing mode (auto → decision based on content)
	actualMode := processingMode
	if processingMode == ProcessingModeAuto {
		actualMode = getAutoProcessingMode(urlStr, originalHTMLSize)
		t.logger.Info().
			Str("url", urlStr).
			Str("requested_mode", string(processingMode)).
			Str("actual_mode", string(actualMode)).
			Int("html_size", originalHTMLSize).
			Msg("Auto-processing mode decision")
	}

	// For preview mode, extract structure only
	if actualMode == ProcessingModePreview {
		return t.buildPreviewResult(ctx, html, urlStr, finalURL, title, duration)
	}

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

	// Decide whether to include screenshot based on processing mode
	includeScreenshot := shouldScreenshot
	if len(screenshotData) > 0 {
		switch actualMode {
		case ProcessingModeFast:
			// Never include screenshot in fast mode
			includeScreenshot = false
		case ProcessingModePreview:
			// Never include screenshot in preview mode (shouldn't reach here)
			includeScreenshot = false
		case ProcessingModeThorough:
			// Always include screenshot if available
			includeScreenshot = true
		case ProcessingModeBalanced:
			// Include based on screenshot_mode parameter
			if screenshotMode == "auto" {
				// Original HTML size decision
				includeScreenshot = originalHTMLSize > 50*1024
			} else {
				includeScreenshot = shouldScreenshot
			}
		case ProcessingModeAuto:
			// Should have been resolved already, but fallback to balanced logic
			if screenshotMode == "auto" {
				includeScreenshot = originalHTMLSize > 50*1024
			} else {
				includeScreenshot = shouldScreenshot
			}
		}

		if includeScreenshot {
			t.logger.Info().
				Str("processing_mode", string(actualMode)).
				Bool("include_screenshot", true).
				Msg("Screenshot included based on processing mode")
		}
	}

	// Extract specific sections if requested
	if len(extractSections) > 0 {
		html = t.extractSectionsByHTML(html, extractSections)
		t.logger.Info().
			Int("sections_requested", len(extractSections)).
			Int("extracted_size", len(html)).
			Msg("Extracted specific sections from HTML")
	}

	// Check max size limit
	if maxSize > 0 && len(html) > maxSize {
		t.logger.Info().
			Int("max_size", maxSize).
			Int("actual_size", len(html)).
			Int("truncated", len(html)-maxSize).
			Msg("HTML exceeds max_size, truncating")

		// Return preview with truncation notice instead of truncating blindly
		return t.buildTruncatedResult(ctx, html, maxSize, urlStr, finalURL, title, duration, originalHTMLSize)
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

	return result, nil
}

// buildPreviewResult creates a preview response with only structural information
func (t *ScrapeJSTool) buildPreviewResult(ctx context.Context, html, url, finalURL, title string, duration time.Duration) (map[string]interface{}, error) {
	// Extract structural information
	sections := t.extractSections(html)
	wordCount := len(strings.Fields(t.cleanHTML(html)))
	readingTime := wordCount / 200 // Average 200 words per minute

	// Detect content type
	contentType := t.detectContentType(html, url)

	// Build preview text
	var previewText strings.Builder
	previewText.WriteString(fmt.Sprintf("# Preview: %s\n\n", title))
	previewText.WriteString(fmt.Sprintf("**URL:** %s\n", url))
	previewText.WriteString(fmt.Sprintf("**Content Type:** %s\n", contentType))
	previewText.WriteString(fmt.Sprintf("**Sections:** %d\n", len(sections)))
	previewText.WriteString(fmt.Sprintf("**Word Count:** %d\n", wordCount))
	previewText.WriteString(fmt.Sprintf("**Estimated Reading Time:** %d min\n\n", readingTime))

	if len(sections) > 0 {
		previewText.WriteString("**Structure:**\n\n")
		for i, section := range sections {
			prefix := strings.Repeat("  ", section["level_int"].(int))
			previewText.WriteString(fmt.Sprintf("%s%d. %s\n", prefix, i+1, section["title"]))
		}
	}

	// Build result
	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": previewText.String(),
			},
		},
		"_metadata": map[string]interface{}{
			"url":              url,
			"final_url":        finalURL,
			"status_code":      200,
			"content_type":     "text/html",
			"size_bytes":       len(html),
			"duration_ms":      duration.Milliseconds(),
			"title":            title,
			"rendering":        "javascript",
			"processing_mode":  "preview",
			"word_count":       wordCount,
			"section_count":    len(sections),
			"content_type_det": contentType,
			"reading_time_min": readingTime,
		},
	}

	t.logger.Info().
		Str("url", url).
		Int("sections", len(sections)).
		Int("word_count", wordCount).
		Str("content_type", contentType).
		Msg("Preview mode completed")

	return result, nil
}

// extractSections extracts heading structure from HTML
func (t *ScrapeJSTool) extractSections(html string) []map[string]interface{} {
	// Simple regex-based extraction
	patterns := []struct {
		level   int
		pattern string
	}{
		{1, `<h1[^>]*>(.+?)</h1>`},
		{2, `<h2[^>]*>(.+?)</h2>`},
		{3, `<h3[^>]*>(.+?)</h3>`},
		{4, `<h4[^>]*>(.+?)</h4>`},
		{5, `<h5[^>]*>(.+?)</h5>`},
		{6, `<h6[^>]*>(.+?)</h6>`},
	}

	var sections []map[string]interface{}
	cleanTitle := func(text string) string {
		// Remove HTML tags
		re := regexp.MustCompile(`<[^>]+>`)
		text = re.ReplaceAllString(text, " ")
		// Clean up whitespace
		return strings.Join(strings.Fields(text), " ")
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				title := cleanTitle(match[1])
				if title != "" {
					sections = append(sections, map[string]interface{}{
						"level":     fmt.Sprintf("h%d", p.level),
						"level_int": p.level - 1,
						"title":     title,
					})
				}
			}
		}
	}

	return sections
}

// detectContentType detects the type of content (article, docs, api, etc)
func (t *ScrapeJSTool) detectContentType(html, url string) string {
	lowerHTML := strings.ToLower(html)
	lowerURL := strings.ToLower(url)

	// Check URL patterns first
	if strings.Contains(lowerURL, "github.com") {
		return "repository"
	}
	if strings.Contains(lowerURL, "docs.") || strings.Contains(lowerURL, "documentation") {
		return "documentation"
	}
	if strings.Contains(lowerURL, "api.") || strings.Contains(lowerURL, "/api/") {
		return "api"
	}
	if strings.Contains(lowerURL, "blog.") || strings.Contains(lowerURL, "/blog/") {
		return "blog"
	}
	if strings.Contains(lowerURL, "news.") {
		return "news"
	}

	// Check HTML patterns
	if strings.Contains(lowerHTML, "article") || strings.Contains(lowerHTML, "post") {
		return "article"
	}
	if strings.Contains(lowerHTML, "docs") || strings.Contains(lowerHTML, "documentation") {
		return "documentation"
	}
	if strings.Contains(lowerHTML, "api") {
		return "api"
	}

	return "general"
}

// cleanHTML removes all HTML tags
func (t *ScrapeJSTool) cleanHTML(text string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

// extractSectionsByHTML extracts specific sections from HTML based on titles
func (t *ScrapeJSTool) extractSectionsByHTML(html string, sections []string) string {
	if len(sections) == 0 {
		return html
	}

	// Build a map of requested sections (lowercase for case-insensitive matching)
	sectionMap := make(map[string]bool)
	for _, section := range sections {
		sectionMap[strings.ToLower(section)] = true
	}

	// Extract all headings with their content
	lines := strings.Split(html, "\n")
	var result []string
	inRequestedSection := false
	capturedContent := false

	for _, line := range lines {
		// Check if this line is a heading
		if strings.HasPrefix(line, "<h1>") || strings.HasPrefix(line, "<h2>") ||
		   strings.HasPrefix(line, "<h3>") || strings.HasPrefix(line, "<h4>") ||
		   strings.HasPrefix(line, "<h5>") || strings.HasPrefix(line, "<h6>") {

			// Extract heading title
			re := regexp.MustCompile(`<h[1-6][^>]*>(.+?)</h[1-6]>`)
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				title := strings.ToLower(t.cleanHTML(matches[1]))

				// Check if this is a requested section
				if sectionMap[title] {
					// Start capturing this section
					inRequestedSection = true
					capturedContent = false
					result = append(result, line) // Add heading
					continue
				}
			}

			// Different heading, stop capturing if we were
			if inRequestedSection && capturedContent {
				inRequestedSection = false
			}
		}

		// Capture content if we're in a requested section
		if inRequestedSection {
			result = append(result, line)
			if strings.TrimSpace(line) != "" {
				capturedContent = true
			}
		}
	}

	// Return original if no sections found
	if len(result) == 0 {
		t.logger.Warn().
			Str("sections_requested", fmt.Sprintf("%v", sections)).
			Msg("No matching sections found, returning original HTML")
		return html
	}

	extracted := strings.Join(result, "\n")
	t.logger.Info().
		Int("original_size", len(html)).
		Int("extracted_size", len(extracted)).
		Int("reduction", len(html)-len(extracted)).
		Float64("reduction_percent", float64(len(html)-len(extracted))/float64(len(html))*100).
		Msg("Sections extracted successfully")

	return extracted
}

// buildTruncatedResult builds a result when HTML exceeds max_size
func (t *ScrapeJSTool) buildTruncatedResult(ctx context.Context, html string, maxSize int, url, finalURL, title string, duration time.Duration, originalSize int) (map[string]interface{}, error) {
	// Build preview with truncation notice
	sections := t.extractSections(html)
	wordCount := len(strings.Fields(t.cleanHTML(html)))

	var previewText strings.Builder
	previewText.WriteString(fmt.Sprintf("# Content Truncated\n\n"))
	previewText.WriteString(fmt.Sprintf("**URL:** %s\n", url))
	previewText.WriteString(fmt.Sprintf("**Title:** %s\n\n", title))
	previewText.WriteString(fmt.Sprintf("⚠️ **Content too large:** %d bytes (limit: %d bytes)\n\n", originalSize, maxSize))
	previewText.WriteString(fmt.Sprintf("**Statistics:**\n"))
	previewText.WriteString(fmt.Sprintf("- Word count: %d\n", wordCount))
	previewText.WriteString(fmt.Sprintf("- Sections: %d\n", len(sections)))
	previewText.WriteString(fmt.Sprintf("\n**Recommendations:**\n"))
	previewText.WriteString(fmt.Sprintf("1. Use `processing_mode: \"preview\"` to see structure first\n"))
	previewText.WriteString(fmt.Sprintf("2. Use `extract_sections: [\"Section Name\"]` to get specific sections\n"))
	previewText.WriteString(fmt.Sprintf("3. Increase `max_size` parameter if needed\n\n"))

	if len(sections) > 0 && len(sections) <= 20 {
		previewText.WriteString("**Available Sections:**\n\n")
		for i, section := range sections {
			prefix := strings.Repeat("  ", section["level_int"].(int))
			previewText.WriteString(fmt.Sprintf("%s%d. %s\n", prefix, i+1, section["title"]))
		}
	} else if len(sections) > 20 {
		previewText.WriteString(fmt.Sprintf("**Available Sections:** %d sections (use preview mode for details)\n", len(sections)))
	}

	// Build result
	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": previewText.String(),
			},
		},
		"_metadata": map[string]interface{}{
			"url":             url,
			"final_url":       finalURL,
			"status_code":     200,
			"content_type":    "text/html",
			"size_bytes":      len(html),
			"original_size":   originalSize,
			"max_size":        maxSize,
			"truncated":       true,
			"duration_ms":     duration.Milliseconds(),
			"title":           title,
			"rendering":       "javascript",
			"word_count":      wordCount,
			"section_count":   len(sections),
			"processing_mode": "truncated",
		},
	}

	t.logger.Info().
		Str("url", url).
		Int("original_size", originalSize).
		Int("max_size", maxSize).
		Int("sections", len(sections)).
		Msg("Content truncated, returning preview with recommendations")

	return result, nil
}
