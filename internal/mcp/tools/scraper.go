package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type ScrapeTool struct {
	*BaseTool
	client *http.Client
	cache  *cache.Cache
	logger zerolog.Logger
}

func NewScrapeTool(cache *cache.Cache) *ScrapeTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"format":      "uri",
				"description": "The URL to scrape",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout in seconds (default: 30)",
				"default":     30,
			},
			"user_agent": map[string]interface{}{
				"type":        "string",
				"description": "Custom user agent string",
			},
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "Custom HTTP headers",
			},
		},
		"required": []string{"url"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &ScrapeTool{
			cache:  cache,
			logger: logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	return &ScrapeTool{
		BaseTool: NewBaseTool(
			"scrape_url",
			"Scrapes a URL and returns HTML in the 'html' field (NOT 'content'). Use for known URLs, then process with smart_extract. Returns: url, status_code, content_type, html, size_bytes, duration_ms, headers",
			schema,
			handler,
		),
	}
}

func (t *ScrapeTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Extract URL
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, fmt.Errorf("url is required and must be a string")
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

			return map[string]interface{}{
				"content": content,
				"_metadata": map[string]interface{}{
					"url":          urlStr,
					"status_code":  200,
					"content_type": cached.Headers["content_type"],
					"size_bytes":   len(cached.Data),
					"cached":       true,
					"duration_ms":  0,
				},
			}, nil
		}
	}

	// Validate URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("only http and https schemes are supported")
	}

	// Extract timeout
	timeout := 30 * time.Second
	if timeoutSec, ok := args["timeout"].(float64); ok {
		timeout = time.Duration(timeoutSec) * time.Second
	}

	// Create HTTP client
	t.client = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Custom user agent
	if userAgent, ok := args["user_agent"].(string); ok && userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	} else {
		req.Header.Set("User-Agent", "MCP-Web-Scrape/1.0 (+https://github.com/metall/mcp-web-scrape)")
	}

	// Custom headers
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for key, value := range headers {
			if strValue, ok := value.(string); ok {
				req.Header.Set(key, strValue)
			}
		}
	}

	// Log request
	t.logger.Info().
		Str("url", urlStr).
		Str("method", "GET").
		Msg("Scraping URL")

	// Execute request
	startTime := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	duration := time.Since(startTime)

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Get content type
	contentType := resp.Header.Get("Content-Type")

	// Optimize HTML for LLM processing - remove noise, keep content
	if strings.Contains(contentType, "text/html") {
		// Check if GitHub for specialized optimization
		if strings.Contains(urlStr, "github.com") {
			body = t.optimizeGitHubHTML(body)
		} else {
			body = t.optimizeHTML(body)
		}
		t.logger.Info().
			Int("original_size", len(body)).
			Msg("HTML optimized for inference")
	}

	// Build result in MCP format
	content := []map[string]interface{}{
		{
			"type": "text",
			"text": string(body),
		},
	}

	result := map[string]interface{}{
		"content": content,
		"_metadata": map[string]interface{}{
			"url":          urlStr,
			"status_code":  resp.StatusCode,
			"content_type": contentType,
			"size_bytes":   len(body),
			"duration_ms":  duration.Milliseconds(),
		},
	}

	// Try to extract title from HTML
	if strings.Contains(contentType, "text/html") {
		if title := extractTitle(string(body)); title != "" {
			result["title"] = title
		}
	}

	// Store in cache
	if t.cache != nil && t.cache.IsEnabled() {
		cacheKey := t.getCacheKey(urlStr, args)
		ttl := t.cache.GetTTLForContentType(contentType)

		cachedResp := &cache.CachedResponse{
			Data:      body,
			Timestamp: time.Now(),
			Headers: map[string]string{
				"content_type":   contentType,
				"content_length": resp.Header.Get("Content-Length"),
				"last_modified":  resp.Header.Get("Last-Modified"),
			},
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

	t.logger.Info().
		Str("url", urlStr).
		Int("status", resp.StatusCode).
		Int("size_bytes", len(body)).
		Int64("duration_ms", duration.Milliseconds()).
		Msg("Successfully scraped URL")

	return result, nil
}

// extractTitle extracts the title from HTML content
func extractTitle(html string) string {
	// Simple title extraction - for production, use a proper HTML parser
	const startTag = "<title>"
	const endTag = "</title>"

	startIdx := strings.Index(strings.ToLower(html), startTag)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startTag)

	endIdx := strings.Index(html[startIdx:], endTag)
	if endIdx == -1 {
		return ""
	}

	title := html[startIdx : startIdx+endIdx]
	return strings.TrimSpace(title)
}

// getCacheKey generates a cache key based on URL and parameters
func (t *ScrapeTool) getCacheKey(url string, args map[string]interface{}) string {
	hash := sha256.New()
	hash.Write([]byte(url))

	// Include custom headers in hash if present
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			hash.Write([]byte(k + ":" + fmt.Sprint(v)))
		}
	}

	return "scrape:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// optimizeHTML removes noise from HTML to reduce token count for inference
func (t *ScrapeTool) optimizeHTML(html []byte) []byte {
	htmlStr := string(html)

	// Remove entire head section (stylesheets, scripts, meta tags)
	headRegex := regexp.MustCompile(`(?is)<head>.*?</head>`)
	htmlStr = headRegex.ReplaceAllString(htmlStr, "")

	// Remove script tags and their content
	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStr = scriptRegex.ReplaceAllString(htmlStr, "")

	// Remove style tags and their content
	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlStr = styleRegex.ReplaceAllString(htmlStr, "")

	// Remove HTML comments
	commentRegex := regexp.MustCompile(`<!--.*?-->`)
	htmlStr = commentRegex.ReplaceAllString(htmlStr, "")

	// Remove link tags (stylesheets, DNS prefetch, etc)
	linkRegex := regexp.MustCompile(`<link[^>]+>`)
	htmlStr = linkRegex.ReplaceAllString(htmlStr, "")

	// Remove meta tags (all except important ones like description/keywords)
	metaRegex := regexp.MustCompile(`<meta(?!\s+(?:name="description"|name="keywords"|property="og:|name="twitter:))[^>]+>`)
	htmlStr = metaRegex.ReplaceAllString(htmlStr, "")

	// Remove UI elements that don't contain content
	uiElements := []struct {
		pattern string
		reason  string
	}{
		// Navigation
		{`(?is)<nav[^>]*>.*?</nav>`, "navigation"},
		{`(?is)<header[^>]*>.*?</header>`, "header"},
		{`(?is)<footer[^>]*>.*?</footer>`, "footer"},

		// Buttons and forms
		{`(?is)<button[^>]*>.*?</button>`, "buttons"},
		{`(?is)<input[^>]+>`, "inputs"},
		{`(?is)<textarea[^>]*>.*?</textarea>`, "textareas"},
		{`(?is)<select[^>]*>.*?</select>`, "selects"},

		// Loaders and placeholders
		{`<div[^>]*class="[^"]*\b(?:loading|skeleton|spinner|placeholder)\b[^"]*"[^>]*>.*?</div>`, "loaders"},

		// Iframes and embeds
		{`(?is)<iframe[^>]*>.*?</iframe>`, "iframes"},
		{`(?is)<embed[^>]+>`, "embeds"},
		{`(?is)<object[^>]*>.*?</object>`, "objects"},

		// Templates and lazy-loaded fragments
		{`<template[^>]*>.*?</template>`, "templates"},
		{`<include-fragment[^>]*>.*?</include-fragment>`, "fragments"},

		// SVG icons
		{`(?is)<svg[^>]*>.*?</svg>`, "svg icons"},

		// Noscript tags
		{`(?is)<noscript[^>]*>.*?</noscript>`, "noscript"},

		// Details/summary (accordions, dropdowns)
		{`(?is)<details[^>]*>.*?</details>`, "dropdowns"},
	}

	for _, p := range uiElements {
		re := regexp.MustCompile(p.pattern)
		htmlStr = re.ReplaceAllString(htmlStr, "")
	}

	// Remove ALL span tags (mostly inline wrappers)
	spanRegex := regexp.MustCompile(`(?is)</?span[^>]*>`)
	htmlStr = spanRegex.ReplaceAllString(htmlStr, "")

	// Remove empty divs
	emptyDivRegex := regexp.MustCompile(`<div[^>]*>\s*</div>`)
	htmlStr = emptyDivRegex.ReplaceAllString(htmlStr, "")

	// Simplify attributes - remove all data-* attributes
	dataAttrRegex := regexp.MustCompile(`\s+data-[a-z0-9-]+="[^"]*"`)
	htmlStr = dataAttrRegex.ReplaceAllString(htmlStr, "")

	// Remove aria attributes (keep content, remove accessibility metadata)
	ariaAttrRegex := regexp.MustCompile(`\s+aria-[a-z0-9-]+="[^"]*"`)
	htmlStr = ariaAttrRegex.ReplaceAllString(htmlStr, "")

	// Remove id attributes
	idAttrRegex := regexp.MustCompile(`\s+id="[^"]*"`)
	htmlStr = idAttrRegex.ReplaceAllString(htmlStr, "")

	// Simplify class attributes (remove utility classes, keep semantic)
	simplifyClassRegex := regexp.MustCompile(`\s+class="[^"]*\b(?:btn|nav|footer|header|sidebar|wrapper|container|loading|skeleton|spinner)\b[^"]*"`)
	htmlStr = simplifyClassRegex.ReplaceAllString(htmlStr, "")

	// Collapse whitespace
	htmlStr = regexp.MustCompile(`\s+`).ReplaceAllString(htmlStr, " ")
	htmlStr = regexp.MustCompile(`>\s+<`).ReplaceAllString(htmlStr, "><")
	htmlStr = strings.TrimSpace(htmlStr)

	return []byte(htmlStr)
}

// optimizeGitHubHTML applies GitHub-specific optimizations on top of general
func (t *ScrapeTool) optimizeGitHubHTML(html []byte) []byte {
	htmlStr := string(html)

	// Apply general optimization first
	htmlStr = string(t.optimizeHTML(html))

	// GitHub-specific removals
	githubPatterns := []struct {
		pattern string
		reason  string
	}{
		// GitHub-specific UI components
		{`<div[^>]*class="[^"]*\bPageLayout-PaneWrapper\b[^"]*"[^>]*>.*?</div>`, "sidebar panels"},
		{`<div[^>]*class="[^"]*\breact-directory\b[^"]*"[^>]*>.*?</div>`, "react directory"},
		{`(?is)<rails-partial[^>]*>.*?</rails-partial>`, "rails partials"},
		{`<li[^>]*class="[^"]*\bNavLink\b[^"]*"[^>]*>.*?</li>`, "nav links"},

		// GitHub skeleton loaders (specific class names)
		{`<div[^>]*class="[^"]*\bSkeleton\b[^"]*"[^>]*>.*?</div>`, "skeleton loaders"},

		// Empty utility containers
		{`<div[^>]*class="[^"]*\b(?:overflow-hidden|d-flex|flex-.*?|Box\b)[^"]*"[^>]*>\s*</div>`, "empty utility divs"},
	}

	for _, p := range githubPatterns {
		re := regexp.MustCompile(p.pattern)
		htmlStr = re.ReplaceAllString(htmlStr, "")
	}

	// Final cleanup
	htmlStr = regexp.MustCompile(`\s+`).ReplaceAllString(htmlStr, " ")
	htmlStr = regexp.MustCompile(`>\s+<`).ReplaceAllString(htmlStr, "><")
	htmlStr = strings.TrimSpace(htmlStr)

	return []byte(htmlStr)
}
