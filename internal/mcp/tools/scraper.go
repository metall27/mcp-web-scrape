package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

type ScrapeTool struct {
	*BaseTool
	scraper   Scraper
	cache     *cache.Cache
	uaRotator *useragent.Rotator
	proxy     *proxy.Rotator
	logger    zerolog.Logger
}

func NewScrapeTool(cache *cache.Cache, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *ScrapeTool {
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
			"output_format": map[string]interface{}{
				"type":        "string",
				"description": "Output format: markdown (default, ~75% smaller, better for LLMs) or html (raw HTML)",
				"enum":        []string{"html", "markdown"},
				"default":     "markdown",
			},
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &ScrapeTool{
			cache:     cache,
			uaRotator: uaRotator,
			proxy:     proxy,
			logger:    logger.Get(),
		}
		httpScraper := NewHTTPScraper(cache, uaRotator, proxy)
		tool.scraper = NewRetryScraper(httpScraper, DefaultRetryConfig)
		return tool.execute(ctx, args)
	}

	return &ScrapeTool{
		BaseTool: NewBaseTool(
			"scrape_url",
			"Fast HTTP scraper for static pages. Use for blogs, news, documentation, and simple HTML sites. For dynamic sites (SPAs, dashboards, JS-rendered content) use scrape_with_js instead.\n\nReturns the page content as Markdown (default, ~75% smaller) or raw HTML. Automatic retry with exponential backoff on transient errors (timeouts, network failures). Detects blocking and returns diagnostic hints. Strips scripts/styles/navigation to reduce output size.",
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

	// Extract timeout
	timeout := 30 * time.Second
	if timeoutSec, ok := args["timeout"].(float64); ok {
		timeout = time.Duration(timeoutSec) * time.Second
	}

	// Extract user agent
	userAgent := ""
	if ua, ok := args["user_agent"].(string); ok {
		userAgent = ua
	}

	// Extract output format (default: markdown for ~75% token savings)
	outputFormat := "markdown"
	if of, ok := args["output_format"].(string); ok && of != "" {
		outputFormat = of
	}

	// Execute scrape using HTTPScraper
	result, err := t.scraper.Scrape(ctx, urlStr, Options{
		Timeout:      timeout,
		UserAgent:    userAgent,
		OutputFormat: outputFormat,
	})

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

	metadata := map[string]interface{}{
		"url":          result.URL,
		"status_code":  result.StatusCode,
		"content_type": result.ContentType,
		"size_bytes":   result.SizeBytes,
		"duration_ms":  result.Duration.Milliseconds(),
		"method":       result.Method,
	}

	if result.FromCache {
		metadata["cached"] = true
		metadata["duration_ms"] = 0
	}

	if result.Title != "" {
		metadata["title"] = result.Title
	}

	return map[string]interface{}{
		"content":   content,
		"_metadata": metadata,
	}, nil
}
