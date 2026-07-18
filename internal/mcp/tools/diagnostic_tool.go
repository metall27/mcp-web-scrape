package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
)

type DiagnosticURLTool struct {
	*BaseTool
	cache        *cache.Cache
	browserPool  *browser.Pool
	ragConfig    config.RAGConfig
	browserCfg   config.BrowserConfig
	uaRotator    *useragent.Rotator
	proxyRotator *proxy.Rotator
	githubCfg    config.GitHubConfig
}

func NewDiagnosticURLTool(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, browserCfg config.BrowserConfig, uaRotator *useragent.Rotator, proxyRotator *proxy.Rotator, githubCfg config.GitHubConfig) *DiagnosticURLTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"format":      "uri",
				"description": "The URL to diagnose",
			},
			"user_agent": map[string]interface{}{
				"type":        "string",
				"description": "Custom user agent string (optional)",
			},
		},
		"required": []string{"url"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &DiagnosticURLTool{}
		return tool.Execute(ctx, args)
	}

	tool := &DiagnosticURLTool{
		cache:        cache,
		browserPool:  browserPool,
		ragConfig:    ragConfig,
		browserCfg:   browserCfg,
		uaRotator:    uaRotator,
		proxyRotator: proxyRotator,
		githubCfg:    githubCfg,
	}

	tool.BaseTool = NewBaseTool(
		"diagnostic_url",
		"Diagnose why scraping a URL fails or is slow. Probes the URL with both HTTP and Chrome and reports: whether it's accessible, whether anti-bot blocking (Cloudflare, captcha) is detected, whether JS rendering is required, and recommends which scraper to use (http vs chrome) and what action to take (retry, screenshot, give up).\n\nUse this after a scrape_url or scrape_with_js call returns a timeout, empty body, or suspected block — to choose the right recovery path instead of guessing.",
		schema,
		handler,
	)

	return tool
}

func (t *DiagnosticURLTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Parse arguments
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("URL is required")
	}

	// Create scrapers for diagnostic (they need to be created per request)
	// Note: We use the configuration stored when tool was created
	httpScraper := NewHTTPScraper(t.cache, t.uaRotator, t.proxyRotator)
	chromeScraper := NewChromeScraper(t.cache, t.browserPool, t.ragConfig, t.browserCfg, t.uaRotator, t.proxyRotator, t.githubCfg)

	// Run diagnostic
	startTime := time.Now()
	result := DiagnosticURL(ctx, httpScraper, chromeScraper, url)
	duration := time.Since(startTime)

	// Build response
	response := map[string]interface{}{
		"url":                    url,
		"accessible":             result.Accessible,
		"status_code":            result.StatusCode,
		"response_time_ms":       result.ResponseTimeMs,
		"blocking_detected":      result.BlockingDetected,
		"requires_javascript":    result.RequiresJavaScript,
		"suggested_scraper":      result.SuggestedScraper,
		"suggested_action":       result.SuggestedAction,
		"issue":                  result.Issue,
		"diagnostic_duration_ms": duration.Milliseconds(),
	}

	if result.BlockingDetected {
		response["blocking_type"] = result.BlockingType
	}

	if len(result.Details) > 0 {
		response["details"] = result.Details
	}

	// Convert to JSON for clean output
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal diagnostic result: %w", err)
	}

	return BuildMCPResponse(string(jsonData), map[string]interface{}{
		"diagnostic_duration_ms": duration.Milliseconds(),
	})
}
