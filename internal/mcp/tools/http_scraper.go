package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

	s.logger.Info().Msg("🚨 HTTPScraper.Scrape CALLED")

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

	// 2.5. Platform Detection and API Fallback (before normal HTTP scraping)
	// GitHub, GitLab, and Gitea use advanced bot detection that defeats standard HTTP scraping
	// Use special API endpoints that work without authentication
	isGitHub := strings.Contains(urlStr, "github.com")
	isGitea := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`).MatchString(urlStr)
	isGitLab := strings.Contains(urlStr, "gitlab.com") || regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`).MatchString(urlStr)

	s.logger.Info().Msg("🚨 PLATFORM DETECTION RUNNING")

	s.logger.Info().
		Str("url", urlStr).
		Bool("is_github", isGitHub).
		Bool("is_gitea", isGitea).
		Bool("is_gitlab", isGitLab).
		Msg("🔍 Platform detection debug")

	if isGitea {
		s.logger.Info().Msg("✅ GITEA DETECTED! EXECUTING API FALLBACK!")
	}

	if isGitHub || isGitea || isGitLab {
		platform := "GitHub"
		if isGitea {
			platform = "Gitea"
		} else if isGitLab {
			platform = "GitLab"
		}

		s.logger.Info().
			Str("url", urlStr).
			Str("platform", platform).
			Msg("🎯 Platform detected - using intelligent API mode")

		// Convert platform URL to API endpoint
		apiURL := s.convertPlatformURL(urlStr)
		s.logger.Info().
			Str("original_url", urlStr).
			Str("api_url", apiURL).
			Msg("🔄 Converted platform URL to API endpoint")

		// Use API fallback
		return s.platformAPIFallback(ctx, apiURL, urlStr, platform, startTime)
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

// convertPlatformURL converts platform URLs to their corresponding API endpoints
func (s *HTTPScraper) convertPlatformURL(urlStr string) string {
	// GitHub URL patterns
	if strings.Contains(urlStr, "github.com") {
		// GitHub repo page → API
		if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/?$`).FindStringSubmatch(urlStr); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			return fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
		}

		// GitHub commit page → API
		if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/commit/([a-f0-9]+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			sha := matches[3]
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, sha)
		}

		// GitHub commits list page → API
		if matches := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/commits/([^/]+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			branch := matches[3]
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?sha=%s&per_page=10", owner, repo, branch)
		}
	}

	// GitLab URL patterns
	if strings.Contains(urlStr, "gitlab.com") || regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`).MatchString(urlStr) {
		// Extract base domain for self-hosted GitLab instances
		baseDomain := "gitlab.com"
		if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
			if matches[1] != "gitlab.com" {
				baseDomain = matches[1]
			}
		}

		// GitLab repo page → API
		if matches := regexp.MustCompile(`([^/]+)/([^/]+)/?$`).FindStringSubmatch(urlStr); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			return fmt.Sprintf("https://%s/api/v4/projects/%s%%2F%s", baseDomain, owner, repo)
		}
	}

	// Gitea URL patterns
	if regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`).MatchString(urlStr) {
		// Extract base domain for self-hosted Gitea instances
		baseDomain := "gitea.com"
		if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
			if matches[1] != "gitea.com" && matches[1] != "gitea.io" {
				baseDomain = matches[1]
			}
		}

		// Extract path without domain
		path := urlStr
		if matches := regexp.MustCompile(`https://[^/]+(/.*)`).FindStringSubmatch(urlStr); len(matches) > 0 {
			path = matches[1]
		}

		// IMPORTANT: Check specific patterns FIRST before generic repo pattern
		// Gitea commits page → API
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/commits/branch/([^/]+)`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			branch := matches[3]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/commits?sha=%s&limit=10", baseDomain, owner, repo, branch)
		}

		// Gitea commits page (simple) → API
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/commits/?$`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/commits?limit=10", baseDomain, owner, repo)
		}

		// Gitea commit page → API
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/commit/([a-f0-9]+)`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			sha := matches[3]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/git/commits/%s", baseDomain, owner, repo, sha)
		}

		// Gitea repo page → API (check LAST, as it's most generic)
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/?$`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s", baseDomain, owner, repo)
		}
	}

	return urlStr
}

// platformAPIFallback performs platform-specific API scraping for GitHub, GitLab, and Gitea
func (s *HTTPScraper) platformAPIFallback(ctx context.Context, apiURL, originalURL, platform string, startTime time.Time) (*Result, error) {
	// Create HTTP client for API request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create API request: %w", err)
	}

	// Set headers for platform APIs
	if platform == "GitHub" {
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("User-Agent", "MCP-Web-Scrape/1.0")
	} else if platform == "GitLab" {
		req.Header.Set("Accept", "application/json")
	} else if platform == "Gitea" {
		req.Header.Set("Accept", "application/json")
	}

	s.logger.Info().
		Str("api_url", apiURL).
		Str("platform", platform).
		Msg("🌐 Making API request")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API response: %w", err)
	}

	// Check for API errors
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s API failed with status %d", platform, resp.StatusCode)
	}

	// Parse JSON response
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		s.logger.Warn().
			Str("error", err.Error()).
			Msg("Failed to parse API response as JSON, treating as HTML")
		// If not JSON, treat as HTML
		return &Result{
			HTML:        string(body),
			URL:         originalURL,
			FinalURL:    originalURL,
			StatusCode:  resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			Duration:    time.Since(startTime),
			SizeBytes:   len(body),
			FromCache:   false,
			Method:      fmt.Sprintf("%s API", platform),
		}, nil
	}

	// Convert JSON to markdown
	var markdown string
	switch v := data.(type) {
	case map[string]interface{}:
		// Single object (repo info, single commit, etc.)
		markdown = s.convertObjectToMarkdown(v, platform)
	case []interface{}:
		// Array (commits list, issues list, etc.)
		markdown = s.convertArrayToMarkdown(v, platform)
	default:
		markdown = fmt.Sprintf("```\n%s\n```", string(body))
	}

	s.logger.Info().
		Str("platform", platform).
		Int("markdown_size", len(markdown)).
		Msg("✅ Successfully converted API response to markdown")

	return &Result{
		HTML:        markdown,
		URL:         originalURL,
		FinalURL:    originalURL,
		StatusCode:  resp.StatusCode,
		ContentType: "text/markdown",
		Duration:    time.Since(startTime),
		SizeBytes:   len(markdown),
		Format:      "markdown",
		FromCache:   false,
		Method:      fmt.Sprintf("%s API", platform),
	}, nil
}

// convertObjectToMarkdown converts a single JSON object to markdown format
func (s *HTTPScraper) convertObjectToMarkdown(obj map[string]interface{}, platform string) string {
	var md strings.Builder

	if platform == "GitHub" || platform == "Gitea" {
		// Repository info
		if name, ok := obj["name"].(string); ok {
			md.WriteString(fmt.Sprintf("# %s\n\n", name))
		}
		if description, ok := obj["description"].(string); ok && description != "" {
			md.WriteString(fmt.Sprintf("%s\n\n", description))
		}

		// Owner info
		if owner, ok := obj["owner"].(map[string]interface{}); ok {
			if login, ok := owner["login"].(string); ok {
				md.WriteString(fmt.Sprintf("**Owner:** %s\n\n", login))
			}
		}

		// Stats
		md.WriteString("## Repository Stats\n\n")
		if stars, ok := obj["stargazers_count"].(float64); ok {
			md.WriteString(fmt.Sprintf("- ⭐ Stars: %.0f\n", stars))
		}
		if forks, ok := obj["forks_count"].(float64); ok {
			md.WriteString(fmt.Sprintf("- 🔱 Forks: %.0f\n", forks))
		}
		if openIssues, ok := obj["open_issues_count"].(float64); ok {
			md.WriteString(fmt.Sprintf("- 📢 Open Issues: %.0f\n", openIssues))
		}

		// Latest activity
		if updatedAt, ok := obj["updated_at"].(string); ok {
			md.WriteString(fmt.Sprintf("\n**Last Updated:** %s\n", updatedAt))
		}

		// For Gitea mirror info
		if mirrorURL, ok := obj["mirror_url"].(string); ok && mirrorURL != "" {
			md.WriteString(fmt.Sprintf("\n**Mirror of:** %s\n", mirrorURL))
		}
		if mirrorUpdated, ok := obj["mirror_updated"].(string); ok && mirrorUpdated != "" {
			md.WriteString(fmt.Sprintf("**Mirror Updated:** %s\n", mirrorUpdated))
		}
	} else if platform == "GitLab" {
		if name, ok := obj["name"].(string); ok {
			md.WriteString(fmt.Sprintf("# %s\n\n", name))
		}
		if description, ok := obj["description"].(string); ok && description != "" {
			md.WriteString(fmt.Sprintf("%s\n\n", description))
		}
	}

	return md.String()
}

// convertArrayToMarkdown converts an array of JSON objects to markdown format
func (s *HTTPScraper) convertArrayToMarkdown(arr []interface{}, platform string) string {
	var md strings.Builder

	// Special handling for commits arrays
	if len(arr) > 0 {
		if firstCommit, ok := arr[0].(map[string]interface{}); ok {
			if _, hasSha := firstCommit["sha"]; hasSha {
				// This is a commits array (has 'sha' field)
				md.WriteString(fmt.Sprintf("## Recent Commits (%d items)\n\n", len(arr)))

				for i, item := range arr {
					if commit, ok := item.(map[string]interface{}); ok {
						md.WriteString(fmt.Sprintf("### Commit %d\n\n", i+1))

						// SHA and URL
						if sha, ok := commit["sha"].(string); ok {
							md.WriteString(fmt.Sprintf("**SHA:** `%s`\n\n", sha))
							if htmlURL, ok := commit["html_url"].(string); ok {
								md.WriteString(fmt.Sprintf("**URL:** %s\n\n", htmlURL))
							}
						}

						// Commit info
						if commitInfo, ok := commit["commit"].(map[string]interface{}); ok {
							// Author
							if author, ok := commitInfo["author"].(map[string]interface{}); ok {
								if name, ok := author["name"].(string); ok {
									md.WriteString(fmt.Sprintf("**Author:** %s\n", name))
								}
								if date, ok := author["date"].(string); ok {
									md.WriteString(fmt.Sprintf("**Date:** %s\n", date))
								}
							}

							// Message
							if message, ok := commitInfo["message"].(string); ok {
								md.WriteString(fmt.Sprintf("\n**Message:**\n%s\n\n", message))
							}
						}

						md.WriteString("\n---\n\n")
					}
				}

				return md.String()
			}
		}
	}

	// Generic array handling
	md.WriteString(fmt.Sprintf("## %s Results (%d items)\n\n", platform, len(arr)))

	for i, item := range arr {
		if obj, ok := item.(map[string]interface{}); ok {
			md.WriteString(fmt.Sprintf("### %d. ", i+1))

			// Try different field names for title/subject
			var title string
			if t, ok := obj["title"].(string); ok {
				title = t
			} else if t, ok := obj["name"].(string); ok {
				title = t
			} else if t, ok := obj["subject"].(string); ok {
				title = t
			}

			if title != "" {
				md.WriteString(fmt.Sprintf("%s\n\n", title))
			} else {
				md.WriteString("\n")
			}

			// Add description/body if available
			if body, ok := obj["body"].(string); ok && body != "" {
				// Truncate long bodies
				if len(body) > 200 {
					body = body[:200] + "..."
				}
				md.WriteString(fmt.Sprintf("%s\n\n", body))
			}

			// Add metadata
			if createdAt, ok := obj["created_at"].(string); ok {
				md.WriteString(fmt.Sprintf("**Created:** %s\n", createdAt))
			}
			if updatedAt, ok := obj["updated_at"].(string); ok {
				md.WriteString(fmt.Sprintf("**Updated:** %s\n", updatedAt))
			}

			md.WriteString("\n---\n\n")
		}
	}

	return md.String()
}