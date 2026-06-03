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
	httpScraper  *HTTPScraper
	chromeScraper *ChromeScraper
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
		tool.httpScraper = NewHTTPScraper(cache, uaRotator, proxyRotator)
		tool.chromeScraper = NewChromeScraper(cache, browserPool, ragConfig, browserCfg, uaRotator, proxyRotator, githubCfg)
		return tool.Execute(ctx, args)
	}

	tool := &DiagnosticURLTool{}

	tool.BaseTool = NewBaseTool(
		"diagnostic_url",
		"Diagnostic tool when scraping fails or performance is suspicious. Detects: blocking (Cloudflare, captcha), JS requirements, accessibility issues. Returns: suggested scraper (http/chrome) and action (retry/screenshot/give_up). Uses existing scrapers - no duplication.\n\nWhen to use:\n- After scrape timeout errors\n- After empty responses\n- To determine best scraping strategy\n- To investigate slow responses\n\nReturns diagnostic information:\n- accessible: Can URL be scraped\n- blocking_detected: Anti-bot protection detected\n- requires_javascript: Site needs JS rendering\n- suggested_scraper: Which scraper to use (http/chrome)\n- suggested_action: What to do (retry/screenshot/give_up)\n- issue: Description of the problem\n\nExample usage:\n- diagnostic_url?url=https://example.com\n- diagnostic_url?url=https://example.com&user_agent=CustomAgent",
		schema,
		handler,
	)

	return tool
}

func (t *DiagnosticURLTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Parse arguments
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return map[string]interface{}{
			"error": "URL is required",
		}, nil
	}

	userAgent := ""
	if ua, ok := args["user_agent"].(string); ok {
		userAgent = ua
	}

	// Run diagnostic
	startTime := time.Now()
	result := DiagnosticURL(ctx, t.httpScraper, t.chromeScraper, url)
	duration := time.Since(startTime)

	_ = userAgent // TODO: pass userAgent to scrapers in DiagnosticURL

	// Build response
	response := map[string]interface{}{
		"url":                  url,
		"accessible":           result.Accessible,
		"status_code":          result.StatusCode,
		"response_time_ms":     result.ResponseTimeMs,
		"blocking_detected":    result.BlockingDetected,
		"requires_javascript":  result.RequiresJavaScript,
		"suggested_scraper":    result.SuggestedScraper,
		"suggested_action":     result.SuggestedAction,
		"issue":                result.Issue,
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
		return map[string]interface{}{
			"error": fmt.Sprintf("Failed to marshal result: %v", err),
		}, nil
	}

	return map[string]interface{}{
		"content_type": "application/json",
		"data":          string(jsonData),
	}, nil
}
