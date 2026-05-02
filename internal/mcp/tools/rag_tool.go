package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultRAGBaseURL = "https://rag.0x27.ru"
	defaultRAGTimeout = 60 * time.Second
)

// RAGClient represents HTTP client for RAG Research Agent
type RAGClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewRAGClient creates a new RAG client
func NewRAGClient(baseURL string) *RAGClient {
	if baseURL == "" {
		baseURL = defaultRAGBaseURL
	}

	return &RAGClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: defaultRAGTimeout,
		},
	}
}

// RAGSearchRequest represents search request
type RAGSearchRequest struct {
	Query   string                 `json:"query"`
	TopK    int                    `json:"top_k,omitempty"`
	Filters map[string]interface{} `json:"filters,omitempty"`
}

// RAGSearchResult represents a single search result
type RAGSearchResult struct {
	ChunkID  string                 `json:"chunk_id"`
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
	Score    float64                `json:"score"`
}

// RAGSearchResponse represents search response
type RAGSearchResponse struct {
	Results      []RAGSearchResult `json:"results"`
	Query        string            `json:"query"`
	TotalResults int               `json:"total_results"`
	SearchTimeMs int               `json:"search_time_ms"`
}

// RAGIndexRequest represents index request
type RAGIndexRequest struct {
	URL            string `json:"url"`
	ProcessingMode string `json:"processing_mode,omitempty"`
	TTL            int    `json:"ttl,omitempty"`
	ChunkStrategy  string `json:"chunk_strategy,omitempty"`
}

// RAGIndexResponse represents index response
type RAGIndexResponse struct {
	Status          string    `json:"status"`
	DocumentID      string    `json:"document_id"`
	ChunksCreated   int       `json:"chunks_created"`
	IndexTimeMs     int       `json:"index_time_ms"`
	EmbeddingsModel string    `json:"embeddings_model"`
	IndexedAt       string `json:"indexed_at"`
}

// RAGHealthResponse represents health check response
type RAGHealthResponse struct {
	Status        string            `json:"status"`
	Version       string            `json:"version"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	Components    map[string]string `json:"components"`
}

// RAGDocument represents document metadata
type RAGDocument struct {
	DocumentID  string     `json:"document_id"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	ChunksCount int        `json:"chunks_count"`
	IndexedAt   time.Time  `json:"indexed_at"`
	TTL         *int       `json:"ttl,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	SizeBytes   int        `json:"size_bytes"`
}

// RAGDocumentListResponse represents document list response
type RAGDocumentListResponse struct {
	Documents  []RAGDocument `json:"documents"`
	TotalCount int           `json:"total_count"`
}

// Search performs semantic search in RAG
func (c *RAGClient) Search(query string, topK int, filters map[string]interface{}) (*RAGSearchResponse, error) {
	if topK <= 0 {
		topK = 5
	}

	reqBody := RAGSearchRequest{
		Query:  query,
		TopK:   topK,
		Filters: filters,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.HTTPClient.Post(
		c.BaseURL+"/api/v1/search",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result RAGSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// Index indexes a web page in RAG
func (c *RAGClient) Index(url, processingMode string, ttl int) (*RAGIndexResponse, error) {
	reqBody := RAGIndexRequest{
		URL:            url,
		ProcessingMode: processingMode,
		TTL:            ttl,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.HTTPClient.Post(
		c.BaseURL+"/api/v1/index",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("index request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("index failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result RAGIndexResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// Health checks RAG service health
func (c *RAGClient) Health() (*RAGHealthResponse, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/health")
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result RAGHealthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// ListDocuments lists all indexed documents
func (c *RAGClient) ListDocuments() (*RAGDocumentListResponse, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/v1/documents")
	if err != nil {
		return nil, fmt.Errorf("list documents failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list documents failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result RAGDocumentListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// NewRAGSearchTool creates semantic search tool
func NewRAGSearchTool() Tool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (supports English and Russian)",
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results to return (default: 5, max: 20)",
				"minimum":     1,
				"maximum":     20,
			},
			"filters": map[string]interface{}{
				"type":        "object",
				"description": "Optional metadata filters (e.g., {\"url\": \"https://example.com\"})",
			},
		},
		"required": []string{"query"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return nil, fmt.Errorf("query parameter is required")
		}

		topK := 5
		if tk, ok := args["top_k"].(float64); ok {
			topK = int(tk)
		}

		var filters map[string]interface{}
		if f, ok := args["filters"].(map[string]interface{}); ok {
			filters = f
		}

		client := NewRAGClient("")
		result, err := client.Search(query, topK, filters)
		if err != nil {
			return nil, fmt.Errorf("RAG search failed: %w", err)
		}

		return map[string]interface{}{
			"query":          result.Query,
			"total_results":  result.TotalResults,
			"search_time_ms": result.SearchTimeMs,
			"results":        result.Results,
		}, nil
	}

	return NewBaseTool(
		"rag_search",
		"🔍 PRIMARY TOOL for information requests! When user wants information/documentation from URL → ALWAYS use rag_search FIRST. Searches indexed knowledge base semantically. If empty results → then use scrape_with_js. Supports English and Russian. Returns text chunks with similarity scores. Workflow: (1) rag_search FIRST, (2) if empty → scrape_with_js + rag_index, (3) next time rag_search works.",
		schema,
		handler,
	)
}

// NewRAGIndexTool creates document indexing tool
func NewRAGIndexTool() Tool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL of the page to index",
			},
			"processing_mode": map[string]interface{}{
				"type":        "string",
				"description": "Processing mode: structured (default), content, or raw",
				"enum":        []string{"structured", "content", "raw"},
			},
			"ttl": map[string]interface{}{
				"type":        "integer",
				"description": "Time to live in days (default: 7)",
				"minimum":     1,
			},
		},
		"required": []string{"url"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		url, ok := args["url"].(string)
		if !ok || url == "" {
			return nil, fmt.Errorf("url parameter is required")
		}

		processingMode := "structured"
		if pm, ok := args["processing_mode"].(string); ok {
			processingMode = pm
		}

		ttl := 0
		if t, ok := args["ttl"].(float64); ok {
			ttl = int(t)
		}

		client := NewRAGClient("")
		result, err := client.Index(url, processingMode, ttl)
		if err != nil {
			return nil, fmt.Errorf("RAG index failed: %w", err)
		}

		return map[string]interface{}{
			"status":           result.Status,
			"document_id":      result.DocumentID,
			"chunks_created":   result.ChunksCreated,
			"index_time_ms":    result.IndexTimeMs,
			"embeddings_model": result.EmbeddingsModel,
			"indexed_at":       result.IndexedAt,
		}, nil
	}

	return NewBaseTool(
		"rag_index",
		"Index a web page in RAG knowledge base for future semantic search. Use this BEFORE rag_search to add content to the knowledge base. This tool scrapes the URL, extracts content, creates embeddings, and stores in vector database. After indexing, use rag_search to query the content. Processing modes: structured (default, best for docs), content (main content only), raw (full HTML). Default TTL: 7 days.",
		schema,
		handler,
	)
}

// NewRAGHealthTool creates RAG health check tool
func NewRAGHealthTool() Tool {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		client := NewRAGClient("")
		result, err := client.Health()
		if err != nil {
			return nil, fmt.Errorf("RAG health check failed: %w", err)
		}

		return map[string]interface{}{
			"status":         result.Status,
			"version":        result.Version,
			"uptime_seconds": result.UptimeSeconds,
			"components":     result.Components,
		}, nil
	}

	return NewBaseTool(
		"rag_health",
		"Check RAG service health and availability. Returns information about embeddings model, vector database status, and integration. Use this to verify RAG service is working before using rag_search or rag_index.",
		schema,
		handler,
	)
}

// NewRAGListDocumentsTool creates document listing tool
func NewRAGListDocumentsTool() Tool {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		client := NewRAGClient("")
		result, err := client.ListDocuments()
		if err != nil {
			return nil, fmt.Errorf("RAG list documents failed: %w", err)
		}

		return map[string]interface{}{
			"documents":   result.Documents,
			"total_count": result.TotalCount,
		}, nil
	}

	return NewBaseTool(
		"rag_list_documents",
		"List all documents currently indexed in RAG knowledge base. Returns document metadata including URL, title, chunk count, indexing time, and TTL. Use this to see what content is available for rag_search before querying.",
		schema,
		handler,
	)
}
