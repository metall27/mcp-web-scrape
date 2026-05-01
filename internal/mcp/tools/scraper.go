package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

			return map[string]interface{}{
				"url":          urlStr,
				"status_code": 200,
				"content_type": cached.Headers["content_type"],
				"html":         string(cached.Data),
				"size_bytes":   len(cached.Data),
				"cached":       true,
				"duration_ms":  0,
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

	// Limit size to avoid overwhelming LLM (max ~100KB for reliable processing)
	maxSize := int64(100 * 1024) // 100KB
	if int64(len(body)) > maxSize {
		body = body[:maxSize]
	}

	// Get content type
	contentType := resp.Header.Get("Content-Type")

	// Build result
	result := map[string]interface{}{
		"url":         urlStr,
		"status_code": resp.StatusCode,
		"content_type": contentType,
		"html":        string(body),  // Renamed from 'content' to avoid LLM confusion
		"size_bytes":  len(body),
		"duration_ms": duration.Milliseconds(),
		"headers": map[string]string{
			"content_type": contentType,
			"content_length": resp.Header.Get("Content-Length"),
			"last_modified": resp.Header.Get("Last-Modified"),
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
