package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
)

// Common shared functions for all scrapers

// ValidateURL проверяет URL
func ValidateURL(urlStr string) (*url.URL, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("only http and https schemes are supported")
	}

	return parsedURL, nil
}

// GenerateCacheKey генерирует ключ кэша для HTTP scraper
func GenerateCacheKey(url string, params map[string]interface{}) string {
	hash := sha256.New()
	hash.Write([]byte(url))

	// Include parameters in hash
	for key, val := range params {
		hash.Write([]byte(fmt.Sprintf("%s:%v", key, val)))
	}

	return "scrape:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// GenerateCacheKeyJS генерирует ключ кэша для JS scraper
func GenerateCacheKeyJS(url string, params map[string]interface{}) string {
	hash := sha256.New()
	hash.Write([]byte(url))

	// Include relevant parameters in hash for JS scraping
	// Different parameters = different result
	keys := []string{"wait_for", "wait_time", "viewport_width", "viewport_height", "block_images"}
	for _, key := range keys {
		if val, ok := params[key]; ok {
			hash.Write([]byte(fmt.Sprintf("%s:%v", key, val)))
		}
	}

	// Include user agent if custom
	if ua, ok := params["user_agent"].(string); ok && ua != "" {
		hash.Write([]byte("user_agent:" + ua))
	}

	return "scrape_js:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// OptsToMap converts Options to map for cache key generation
func OptsToMap(opts Options) map[string]interface{} {
	result := map[string]interface{}{
		"user_agent": opts.UserAgent,
		"timeout":    opts.Timeout.String(),
		"format":     opts.OutputFormat,
	}

	// Add JS-specific options if relevant
	if opts.WaitForSelector != "" {
		result["wait_for"] = opts.WaitForSelector
	}
	if opts.WaitForDuration > 0 {
		result["wait_time"] = opts.WaitForDuration.String()
	}
	if opts.ViewportWidth > 0 {
		result["viewport_width"] = opts.ViewportWidth
	}
	if opts.ViewportHeight > 0 {
		result["viewport_height"] = opts.ViewportHeight
	}
	if opts.WaitForNetworkIdle {
		result["network_idle"] = true
	}
	if opts.StealthEnabled {
		result["stealth_enabled"] = true
	}
	if opts.StealthScroll {
		result["stealth_scroll"] = true
	}
	if opts.StealthMouse {
		result["stealth_mouse"] = true
	}

	return result
}