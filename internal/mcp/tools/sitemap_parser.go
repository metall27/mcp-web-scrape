package tools

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

// SitemapEntry represents a single URL entry in a sitemap
type SitemapEntry struct {
	Loc        string    `xml:"loc"`
	LastMod    time.Time `xml:"lastmod,omitempty"`
	ChangeFreq string    `xml:"changefreq,omitempty"`
	Priority   float64   `xml:"priority,omitempty"`
}

// Sitemap represents the root sitemap XML structure
type Sitemap struct {
	XMLName xml.Name       `xml:"urlset"`
	Entries []SitemapEntry `xml:"url"`
}

// SitemapIndex represents a sitemap index file (sitemap of sitemaps)
type SitemapIndex struct {
	XMLName xml.Name      `xml:"sitemapindex"`
	Sitemaps []SitemapRef `xml:"sitemap"`
}

// SitemapRef represents a reference to another sitemap
type SitemapRef struct {
	Loc     string `xml:"loc"`
	LastMod time.Time `xml:"lastmod,omitempty"`
}

// SitemapParser handles sitemap.xml parsing and catalog URL discovery
type SitemapParser struct {
	cache       *cache.Cache
	httpScraper *HTTPScraper
	logger      zerolog.Logger
}

// NewSitemapParser creates a new sitemap parser instance
func NewSitemapParser(cache *cache.Cache, httpScraper *HTTPScraper) *SitemapParser {
	return &SitemapParser{
		cache:       cache,
		httpScraper: httpScraper,
		logger:      logger.Get(),
	}
}

// DiscoverSitemapURL attempts to find sitemap at standard locations
func (s *SitemapParser) DiscoverSitemapURL(baseURL string) []string {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		s.logger.Debug().Err(err).Str("base_url", baseURL).Msg("Failed to parse base URL")
		return nil
	}

	base := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Common sitemap locations to try
	sitemapLocations := []string{
		"/sitemap.xml",
		"/sitemap_index.xml",
		"/sitemaps.xml",
		"/wpms-sitemap.xml", // WordPress specific
		"/sitemap/sitemap.xml",
	}

	var validSitemaps []string

	for _, location := range sitemapLocations {
		sitemapURL := base + location
		s.logger.Debug().Str("sitemap_url", sitemapURL).Msg("Trying sitemap location")

		// Quick check if sitemap exists
		if s.validateSitemapURL(sitemapURL) {
			validSitemaps = append(validSitemaps, sitemapURL)
		}
	}

	s.logger.Info().
		Str("base_url", baseURL).
		Int("found_count", len(validSitemaps)).
		Msg("Discovered sitemaps")

	return validSitemaps
}

// validateSitemapURL performs a quick validation check for sitemap URL
func (s *SitemapParser) validateSitemapURL(sitemapURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := s.httpScraper.Scrape(ctx, sitemapURL, Options{
		OutputFormat: "html", // We need raw content
		Timeout:      10 * time.Second,
	})

	if err != nil {
		s.logger.Debug().Err(err).Str("sitemap_url", sitemapURL).Msg("Sitemap validation failed")
		return false
	}

	// Check if content looks like a sitemap
	content := strings.ToLower(result.HTML)
	return strings.Contains(content, "<urlset") ||
	       strings.Contains(content, "<sitemapindex") ||
	       strings.Contains(content, "xmlns=")
}

// ParseSitemap parses a sitemap XML and extracts all entries
func (s *SitemapParser) ParseSitemap(ctx context.Context, sitemapURL string) ([]SitemapEntry, error) {
	cacheKey := fmt.Sprintf("sitemap:%s", sitemapURL)

	// Check cache first
	if cached, exists := s.cache.Get(ctx, cacheKey); exists && cached != nil {
		var entries []SitemapEntry
		if err := xml.Unmarshal(cached.Data, &entries); err == nil {
			s.logger.Debug().Str("sitemap_url", sitemapURL).Msg("Loaded sitemap from cache")
			return entries, nil
		}
	}

	s.logger.Info().Str("sitemap_url", sitemapURL).Msg("Fetching sitemap")

	// Fetch sitemap content
	result, err := s.httpScraper.Scrape(ctx, sitemapURL, Options{
		OutputFormat: "html",
	})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch sitemap: %w", err)
	}

	// Try to parse as sitemap index first
	var sitemapIndex SitemapIndex
	if err := xml.Unmarshal([]byte(result.HTML), &sitemapIndex); err == nil && len(sitemapIndex.Sitemaps) > 0 {
		s.logger.Info().
			Str("sitemap_url", sitemapURL).
			Int("sitemap_count", len(sitemapIndex.Sitemaps)).
			Msg("Found sitemap index, parsing child sitemaps")

		// Parse all child sitemaps
		var allEntries []SitemapEntry
		for _, sitemapRef := range sitemapIndex.Sitemaps {
			childEntries, err := s.ParseSitemap(ctx, sitemapRef.Loc)
			if err != nil {
				s.logger.Warn().
					Err(err).
					Str("child_sitemap", sitemapRef.Loc).
					Msg("Failed to parse child sitemap")
				continue
			}
			allEntries = append(allEntries, childEntries...)
		}

		// Cache the combined result
		if data, err := xml.Marshal(allEntries); err == nil {
			cachedResp := &cache.CachedResponse{
				Data:      data,
				Timestamp: time.Now(),
				Headers:   map[string]string{"Content-Type": "application/xml"},
			}
			_ = s.cache.Set(ctx, cacheKey, cachedResp, 1*time.Hour)
		}

		return allEntries, nil
	}

	// Parse as regular sitemap
	var sitemap Sitemap
	if err := xml.Unmarshal([]byte(result.HTML), &sitemap); err != nil {
		return nil, fmt.Errorf("failed to parse sitemap XML: %w", err)
	}

	s.logger.Info().
		Str("sitemap_url", sitemapURL).
		Int("entry_count", len(sitemap.Entries)).
		Msg("Successfully parsed sitemap")

	// Cache the result
	if data, err := xml.Marshal(sitemap.Entries); err == nil {
		cachedResp := &cache.CachedResponse{
			Data:      data,
			Timestamp: time.Now(),
			Headers:   map[string]string{"Content-Type": "application/xml"},
		}
		_ = s.cache.Set(ctx, cacheKey, cachedResp, 1*time.Hour)
	}

	return sitemap.Entries, nil
}

// FindCatalogURLs finds catalog-related URLs from sitemap entries
func (s *SitemapParser) FindCatalogURLs(entries []SitemapEntry) []string {
	catalogPatterns := []struct {
		pattern    string
		priority   float64
		keywords   []string
	}{
		// High priority: direct catalog paths
		{pattern: `/(catalog|shop|store|products|goods)/`, priority: 1.0, keywords: []string{}},

		// Medium priority: category pages
		{pattern: `/(category|categories|collections|product-category)/`, priority: 0.8, keywords: []string{}},

		// Lower priority: potential product listings
		{pattern: `/(items|listings|inventory)/`, priority: 0.6, keywords: []string{}},
	}

	var catalogURLs []struct {
		url      string
		priority float64
	}

	for _, entry := range entries {
		for _, catalogPat := range catalogPatterns {
			matched, err := regexp.MatchString(catalogPat.pattern, entry.Loc)
			if err != nil {
				continue
			}

			if matched {
				// Check keywords if specified
				if len(catalogPat.keywords) > 0 {
					lowerURL := strings.ToLower(entry.Loc)
					hasKeyword := false
					for _, keyword := range catalogPat.keywords {
						if strings.Contains(lowerURL, keyword) {
							hasKeyword = true
							break
						}
					}
					if !hasKeyword {
						continue
					}
				}

				// Use sitemap priority if available, otherwise use pattern priority
				priority := entry.Priority
				if priority == 0 {
					priority = catalogPat.priority
				}

				catalogURLs = append(catalogURLs, struct {
					url      string
					priority float64
				}{
					url:      entry.Loc,
					priority: priority,
				})

				s.logger.Debug().
					Str("url", entry.Loc).
					Float64("priority", priority).
					Msg("Found catalog URL in sitemap")
			}
		}
	}

	// Sort by priority (highest first)
	for i := 0; i < len(catalogURLs); i++ {
		for j := i + 1; j < len(catalogURLs); j++ {
			if catalogURLs[j].priority > catalogURLs[i].priority {
				catalogURLs[i], catalogURLs[j] = catalogURLs[j], catalogURLs[i]
			}
		}
	}

	// Extract just the URLs
	var result []string
	for _, item := range catalogURLs {
		result = append(result, item.url)
	}

	s.logger.Info().
		Int("total_entries", len(entries)).
		Int("catalog_urls", len(result)).
		Msg("Extracted catalog URLs from sitemap")

	return result
}

// GetBaseDomain extracts the base domain from a URL
func (s *SitemapParser) GetBaseDomain(pageURL string) string {
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
}