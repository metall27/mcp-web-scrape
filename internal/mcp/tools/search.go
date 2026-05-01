package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type SearchTool struct {
	*BaseTool
	client *http.Client
	logger zerolog.Logger
}

func NewSearchTool() *SearchTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query string",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 10)",
				"default":     10,
			},
			"safe_search": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable safe search filtering (default: true)",
				"default":     true,
			},
			"provider": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"duckduckgo", "brave", "bing"},
				"description": "Search provider to use (default: duckduckgo)",
				"default":     "duckduckgo",
			},
		},
		"required": []string{"query"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &SearchTool{
			logger: logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	return &SearchTool{
		BaseTool: NewBaseTool(
			"search_web",
			"Searches the web to FIND URLs. Use to discover new sources or when you don't have a specific URL. For known URLs, use scrape_url + smart_extract instead. DuckDuckGo currently unreliable, prefer scraping known sources.",
			schema,
			handler,
		),
	}
}

func (t *SearchTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Extract query
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required and must be a non-empty string")
	}

	// Extract options
	maxResults := 10
	if mr, ok := args["max_results"].(float64); ok {
		maxResults = int(mr)
		if maxResults < 1 || maxResults > 50 {
			return nil, fmt.Errorf("max_results must be between 1 and 50")
		}
	}

	safeSearch := true
	if ss, ok := args["safe_search"].(bool); ok {
		safeSearch = ss
	}

	provider := "duckduckgo"
	if p, ok := args["provider"].(string); ok && p != "" {
		provider = p
	}

	// Create HTTP client
	t.client = &http.Client{
		Timeout: 30 * time.Second,
	}

	// Perform search based on provider
	var results []SearchResult
	var err error

	switch provider {
	case "duckduckgo":
		results, err = t.searchDuckDuckGo(ctx, query, maxResults)
	case "brave":
		results, err = t.searchBrave(ctx, query, maxResults, safeSearch)
	case "bing":
		results, err = t.searchBing(ctx, query, maxResults, safeSearch)
	default:
		return nil, fmt.Errorf("unsupported search provider: %s", provider)
	}

	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"query":       query,
		"provider":    provider,
		"total_count": len(results),
		"results":     results,
	}

	t.logger.Info().
		Str("query", query).
		Str("provider", provider).
		Int("results", len(results)).
		Msg("Search completed")

	return result, nil
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Source  string `json:"source,omitempty"`
}

// DuckDuckGo search (HTML scraping approach)
func (t *SearchTool) searchDuckDuckGo(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	// Build DuckDuckGo search URL
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch search results: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("DuckDuckGo returned status %d", resp.StatusCode)
	}

	// Parse HTML response (simplified - for production use goquery)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Extract results from HTML
	// This is a simplified approach - for production, use goquery for proper HTML parsing
	results := t.parseDuckDuckGoHTML(string(body), maxResults)

	return results, nil
}

func (t *SearchTool) parseDuckDuckGoHTML(html string, maxResults int) []SearchResult {
	var results []SearchResult

	// Very basic HTML parsing - in production, use goquery
	// This is just a placeholder to show the structure
	// Actual implementation would parse the HTML properly

	// For now, return empty results with a note
	t.logger.Warn().Msg("DuckDuckGo HTML parsing is simplified - use goquery for production")

	return results
}

// Brave Search API
func (t *SearchTool) searchBrave(ctx context.Context, query string, maxResults int, safeSearch bool) ([]SearchResult, error) {
	// Brave Search API requires an API key
	// For now, return an error explaining this
	return nil, fmt.Errorf("Brave Search API requires an API key. Set BRAVE_API_KEY environment variable or use 'duckduckgo' provider")
}

// Bing Search API
func (t *SearchTool) searchBing(ctx context.Context, query string, maxResults int, safeSearch bool) ([]SearchResult, error) {
	// Bing Search API requires an API key
	// For now, return an error explaining this
	return nil, fmt.Errorf("Bing Search API requires an API key. Set BING_API_KEY environment variable or use 'duckduckgo' provider")
}

// Helper function for API-based searches
func (t *SearchTool) doAPISearch(ctx context.Context, apiURL, apiKey string, params map[string]string) ([]SearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add API key header if provided
	if apiKey != "" {
		req.Header.Set("X-Subscription-Token", apiKey)
	}

	req.Header.Set("Accept", "application/json")

	// Add query parameters
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch search results: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response
	var result struct {
		Web struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	// Convert to SearchResult
	var results []SearchResult
	for _, r := range result.Web.Results {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Snippet,
		})
	}

	return results, nil
}

func intToString(i int) string {
	return strconv.Itoa(i)
}
