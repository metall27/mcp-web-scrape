package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DiagnosticResult результат диагностики URL
type DiagnosticResult struct {
	Accessible         bool
	StatusCode         int
	ResponseTimeMs     int64
	BlockingDetected   bool
	BlockingType       string
	RequiresJavaScript bool
	ContentSize        int
	SuggestedScraper   string // "http" or "chrome"
	SuggestedAction    string // "retry", "use_screenshot", "give_up"
	Issue              string
	Details            map[string]string
}

// DiagnosticURL диагностирует URL и возвращает рекомендации
func DiagnosticURL(ctx context.Context, httpScraper, chromeScraper Scraper, url string) *DiagnosticResult {
	result := &DiagnosticResult{
		Details: make(map[string]string),
	}

	startTime := time.Now()

	// 1. Пробовать HTTP scraper first (fast)
	httpResult, httpErr := httpScraper.Scrape(ctx, url, Options{
		Timeout: 30 * time.Second,
	})

	duration := time.Since(startTime)
	result.ResponseTimeMs = duration.Milliseconds()

	if httpErr == nil {
		result.Accessible = true
		result.StatusCode = httpResult.StatusCode
		result.ContentSize = len(httpResult.HTML)
		result.SuggestedScraper = "http"
		result.Issue = ""

		// Проверить если нужен JS
		if requiresJS(httpResult.HTML) {
			result.RequiresJavaScript = true
			result.SuggestedScraper = "chrome"
			result.SuggestedAction = "use_chrome_scraper"
			result.Details["reason"] = "Site appears to require JavaScript rendering"
		} else {
			result.SuggestedAction = "use_http_scraper"
		}

		return result
	}

	// 2. HTTP failed - анализ ошибки
	var httpScrapeErr *ScrapeError
	if errors.As(httpErr, &httpScrapeErr) {
		result.Details["http_error_code"] = httpScrapeErr.Code
		result.Details["http_error_message"] = httpScrapeErr.Message

		if httpScrapeErr.Code == "blocked" {
			result.BlockingDetected = true
			result.BlockingType = detectBlockingType(httpScrapeErr)
			result.Issue = fmt.Sprintf("Anti-bot protection detected: %s", result.BlockingType)
			result.SuggestedAction = "use_screenshot_or_chrome"
			result.SuggestedScraper = "chrome" // Chrome может справиться лучше
			return result
		}
	} else {
		// Fallback для стандартных ошибок
		result.Details["http_error"] = httpErr.Error()
	}

	// 3. Пробовать Chrome scraper (если HTTP failed)
	startTime = time.Now()
	chromeResult, chromeErr := chromeScraper.Scrape(ctx, url, Options{
		Timeout: 30 * time.Second,
	})
	duration = time.Since(startTime)
	result.Details["chrome_duration_ms"] = fmt.Sprintf("%d", duration.Milliseconds())

	if chromeErr == nil {
		result.RequiresJavaScript = true
		result.Accessible = true
		result.StatusCode = chromeResult.StatusCode
		result.ContentSize = len(chromeResult.HTML)
		result.SuggestedScraper = "chrome"
		result.SuggestedAction = "use_chrome_scraper"
		result.Issue = "HTTP scraper failed, but Chrome scraper succeeded"
		return result
	}

	// 4. Оба failed
	var chromeScrapeErr *ScrapeError
	if errors.As(chromeErr, &chromeScrapeErr) {
		result.Details["chrome_error_code"] = chromeScrapeErr.Code
		result.Details["chrome_error_message"] = chromeScrapeErr.Message
	} else {
		result.Details["chrome_error"] = chromeErr.Error()
	}

	result.Accessible = false
	result.Issue = "Both HTTP and Chrome scrapers failed"

	// Анализ причин
	if (httpScrapeErr != nil && httpScrapeErr.Code == "timeout") && (chromeScrapeErr != nil && chromeScrapeErr.Code == "timeout") {
		result.SuggestedAction = "use_screenshot_or_give_up"
		result.Details["reason"] = "Both scrapers timed out"
	} else if (httpScrapeErr != nil && httpScrapeErr.Code == "blocked") || (chromeScrapeErr != nil && chromeScrapeErr.Code == "blocked") {
		result.SuggestedAction = "use_screenshot"
		result.Details["reason"] = "Blocking detected on both scrapers"
	} else {
		result.SuggestedAction = "retry_with_screenshot"
		result.Details["reason"] = "Unknown errors on both scrapers"
	}

	return result
}

// requiresJS проверяет если сайт требует JavaScript
func requiresJS(html string) bool {
	if len(html) == 0 {
		return true // Empty HTML might mean JS required
	}

	lowerHTML := strings.ToLower(html)

	// Check for common JS framework indicators
	jsIndicators := []string{
		"<div id=\"app\">",
		"<div id=\"root\">",
		"react",
		"vue",
		"angular",
		"ng-app",
		"__NEXT_DATA__",
		"__NUXT__",
		"window.__STATE__",
	}

	for _, indicator := range jsIndicators {
		if strings.Contains(lowerHTML, indicator) {
			return true
		}
	}

	// Check if HTML is too small (might be SPA shell)
	if len(html) < 500 && strings.Contains(html, "<html") {
		return true
	}

	// Check for common meta tags
	if strings.Contains(lowerHTML, "<meta name=\"renderer\"") ||
		strings.Contains(lowerHTML, "<meta http-equiv=\"x-ua-compatible\"") {
		// These might indicate dynamic content
	}

	return false
}

// detectBlockingType определяет тип блокировки
func detectBlockingType(err *ScrapeError) string {
	msg := strings.ToLower(err.Message)

	if strings.Contains(msg, "cloudflare") {
		return "cloudflare"
	}
	if strings.Contains(msg, "captcha") {
		return "captcha"
	}
	if strings.Contains(msg, "rate limit") || strings.Contains(msg, "429") {
		return "rate_limit"
	}
	if strings.Contains(msg, "403") {
		return "forbidden"
	}

	return "unknown"
}
