package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

// HTTPScraper скрапер для статических сайтов (HTTP)
type HTTPScraper struct {
	cache     *cache.Cache
	uaRotator *useragent.Rotator
	proxy     *proxy.Rotator
	client    *http.Client
	logger    zerolog.Logger
}

// NewHTTPScraper создает новый HTTPScraper
func NewHTTPScraper(cache *cache.Cache, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *HTTPScraper {
	return &HTTPScraper{
		cache:     cache,
		uaRotator: uaRotator,
		proxy:     proxy,
		logger:    logger.Get(),
	}
}

// Scrape реализует интерфейс Scraper
func (s *HTTPScraper) Scrape(ctx context.Context, urlStr string, opts Options) (*Result, error) {
	startTime := time.Now()

	// 1. Validate URL
	if _, err := ValidateURL(urlStr); err != nil {
		return nil, err
	}

	// 2. Check cache
	if s.cache != nil && s.cache.IsEnabled() {
		cacheKey := GenerateCacheKey(urlStr, OptsToMap(opts))
		if cached, found := s.cache.Get(ctx, cacheKey); found {
			s.logger.Info().
				Str("url", urlStr).
				Str("cache_key", cacheKey).
				Msg("Cache hit")

			return &Result{
				HTML:        string(cached.Data),
				URL:         urlStr,
				ContentType: cached.Headers["content_type"],
				FromCache:   true,
				Method:      s.Name(),
			}, nil
		}
	}

	// 3. Create HTTP client
	client := &http.Client{
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// 4. Add proxy if enabled
	if s.proxy != nil && s.proxy.IsEnabled() {
		client.Transport = &http.Transport{
			Proxy: s.proxy.GetProxyFunc(),
		}
		s.logger.Info().
			Msg("Using proxy for HTTP request")
	}

	// 5. Create request
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 6. Set headers
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Set User-Agent
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	} else if s.uaRotator != nil {
		req.Header.Set("User-Agent", s.uaRotator.Get())
	} else {
		req.Header.Set("User-Agent", "MCP-Web-Scrape/1.0 (+https://github.com/metall/mcp-web-scrape)")
	}

	// 7. Log request
	s.logger.Info().
		Str("url", urlStr).
		Str("method", "GET").
		Msg("Scraping URL")

	// 8. Execute request
	resp, err := client.Do(req)
	if err != nil {
		if s.proxy != nil && s.proxy.IsEnabled() {
			s.proxy.MarkFailure(err)
		}
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// 9. Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if s.proxy != nil && s.proxy.IsEnabled() {
			s.proxy.MarkFailure(fmt.Errorf("HTTP %d", resp.StatusCode))
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Mark proxy as successful
	if s.proxy != nil && s.proxy.IsEnabled() {
		s.proxy.MarkSuccess()
	}

	// 10. Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// 11. Get content type
	contentType := resp.Header.Get("Content-Type")

	// 12. Optimize HTML for LLM processing
	if strings.Contains(contentType, "text/html") {
		// Check if GitHub for specialized optimization
		if strings.Contains(urlStr, "github.com") {
			body = OptimizeGitHubHTML(body)
		} else {
			body = OptimizeHTML(body)
		}
		s.logger.Info().
			Int("optimized_size", len(body)).
			Msg("HTML optimized for inference")
	}

	// 13. Extract title
	title := ""
	if strings.Contains(contentType, "text/html") {
		title = extractTitleFromHTML(string(body))
	}

	// 14. Build result
	result := &Result{
		HTML:        string(body),
		Title:       title,
		URL:         urlStr,
		FinalURL:    urlStr,
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Duration:    time.Since(startTime),
		SizeBytes:   len(body),
		Format:      opts.OutputFormat,
		FromCache:   false,
		Method:      s.Name(),
	}

	// 15. Store in cache
	if s.cache != nil && s.cache.IsEnabled() {
		cacheKey := GenerateCacheKey(urlStr, OptsToMap(opts))
		ttl := s.cache.GetTTLForContentType(contentType)

		cachedResp := &cache.CachedResponse{
			Data:      body,
			Timestamp: time.Now(),
			Headers: map[string]string{
				"content_type":   contentType,
				"content_length": resp.Header.Get("Content-Length"),
				"last_modified":  resp.Header.Get("Last-Modified"),
			},
		}

		if err := s.cache.Set(ctx, cacheKey, cachedResp, ttl); err != nil {
			s.logger.Error().
				Str("cache_key", cacheKey).
				Err(err).
				Msg("Failed to store in cache")
		} else {
			s.logger.Info().
				Str("cache_key", cacheKey).
				Dur("ttl", ttl).
				Msg("Stored in cache")
		}
	}

	s.logger.Info().
		Str("url", urlStr).
		Int("status", resp.StatusCode).
		Int("size_bytes", len(body)).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("Successfully scraped URL")

	return result, nil
}

// Name возвращает название скрапера
func (s *HTTPScraper) Name() string {
	return "HTTP"
}

// SupportsJS возвращает false
func (s *HTTPScraper) SupportsJS() bool {
	return false
}

// SupportsActions возвращает false
func (s *HTTPScraper) SupportsActions() bool {
	return false
}


// extractTitleFromHTML extracts the title from HTML content
func extractTitleFromHTML(html string) string {
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