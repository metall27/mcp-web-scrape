package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type ScrapeTool struct {
	*BaseTool
	client *http.Client
	logger zerolog.Logger
}

func NewScrapeTool() *ScrapeTool {
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
			logger: logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	return &ScrapeTool{
		BaseTool: NewBaseTool(
			"scrape_url",
			"Scrapes content from a URL and returns the HTML/body content",
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

	// Build result
	result := map[string]interface{}{
		"url":         urlStr,
		"status_code": resp.StatusCode,
		"content_type": contentType,
		"content":     string(body),
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
