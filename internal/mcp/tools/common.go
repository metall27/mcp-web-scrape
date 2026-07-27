package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// BuildMCPResponse constructs a standard MCP CallToolResult:
//
//	{"content": [{"type":"text","text": <json>}], "_metadata": meta}
//
// All tools MUST use this so every client (Claude Desktop, llama.cpp WebUI,
// Open WebUI) sees content in the same shape. `data` is JSON-serialised into
// the single text content item; `meta` (optional) is passed through verbatim
// under _metadata and may itself be nil.
func BuildMCPResponse(data interface{}, meta map[string]interface{}) (map[string]interface{}, error) {
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool result: %w", err)
	}
	resp := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(payload),
			},
		},
	}
	if meta != nil {
		resp["_metadata"] = meta
	}
	return resp, nil
}

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
	// Core format options
	keys := []string{
		"format",          // html vs markdown (CRITICAL - affects output content)
		"screenshot_mode", // never, auto, always (affects response structure)
		"wait_for",        // CSS selector (affects page state)
		"wait_time",       // delay in ms (affects page state)
		"viewport_width",  // viewport width (affects responsive layout)
		"viewport_height", // viewport height (affects responsive layout)
		"block_images",    // image blocking (affects page content)
		"wait_for_network_idle", // network idle waiting (affects page state)
	}
	for _, key := range keys {
		if val, ok := params[key]; ok {
			hash.Write([]byte(fmt.Sprintf("%s:%v", key, val)))
		}
	}

	// Include user agent if custom (affects some content)
	if ua, ok := params["user_agent"].(string); ok && ua != "" {
		hash.Write([]byte("user_agent:" + ua))
	}

	return "scrape_js:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// OptsToMap converts Options to map for cache key generation
func OptsToMap(opts Options) map[string]interface{} {
	result := map[string]interface{}{
		"user_agent":        opts.UserAgent,
		"timeout":           opts.Timeout.String(),
		"format":            opts.OutputFormat,
		"screenshot_mode":   opts.ScreenshotMode,
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
		result["wait_for_network_idle"] = true
	}
	if opts.BlockImages {
		result["block_images"] = true
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

// sensitiveQueryKeys — список имён query-параметров, значения которых
// маскируются в логах/метаданных, чтобы предотвратить утечку кредов (issue #69).
// Сравнение нечувствительно к регистру.
var sensitiveQueryKeys = map[string]bool{
	"password":     true,
	"passwd":       true,
	"login":        true, // my.rebrainme.com шлёт логин в ?login=
	"email":        true,
	"token":        true,
	"access_token": true,
	"api_key":      true,
	"apikey":       true,
	"secret":       true,
	"auth":         true,
	"session":      true,
}

// redactPlaceholder — значение, подставляемое вместо реального sensitive-параметра.
const redactPlaceholder = "***"

// RedactURL маскирует sensitive query-параметры в URL для безопасного логирования.
//
// Вернёт исходную строку как есть (best-effort), если url.Parse упадёт или
// это не URL вообще. Query-значения ключей из sensitiveQueryKeys заменяются
// на "***", всё остальное сохраняется (scheme/host/path/safe params).
//
// Значения пересобираются вручную через strings.Builder, а не url.Values.Encode()
// (который URL-кодирует "***" в "%2A%2A%2A" — нечитаемо в логах). Сами ключи и
// безопасные значения кодируются через url.QueryEscape для корректности.
//
// Пример:
//
//	RedactURL("https://my.rebrainme.com/?login=dpavlov85@yandex.ru&password=Y2RkNDkx")
//	→ "https://my.rebrainme.com/?login=***&password=***"
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.RawQuery == "" {
		return rawURL
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return rawURL
	}

	// Detect whether any key needs redaction; rebuild RawQuery preserving order.
	// url.Values.Encode() re-encodes "***" → "%2A%2A%2A" which is unreadable in
	// logs, so we assemble the query string ourselves with literal "***".
	needsRedact := false
	var b strings.Builder
	first := true
	for key, vals := range values {
		for _, v := range vals {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(url.QueryEscape(key))
			b.WriteByte('=')
			if sensitiveQueryKeys[strings.ToLower(key)] {
				b.WriteString(redactPlaceholder)
				needsRedact = true
			} else {
				b.WriteString(url.QueryEscape(v))
			}
		}
	}
	if !needsRedact {
		return rawURL
	}
	u.RawQuery = b.String()
	return u.String()
}