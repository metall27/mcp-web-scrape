package openapi

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/metall/mcp-web-scrape/internal/mcp"
	"github.com/rs/zerolog"
)

// Handler handles OpenAPI/REST requests
type Handler struct {
	mcpServer *mcp.Server
	logger    zerolog.Logger
	baseURL   string
}

// NewHandler creates a new OpenAPI handler
func NewHandler(mcpServer *mcp.Server, baseURL string) *Handler {
	return &Handler{
		mcpServer: mcpServer,
		logger:    zerolog.Logger{}, // Will be initialized properly
		baseURL:   baseURL,
	}
}

// SetLogger sets the logger instance
func (h *Handler) SetLogger(logger zerolog.Logger) {
	h.logger = logger
}

// GetOpenAPI spec handler
func (h *Handler) GetOpenAPI(c *gin.Context) {
	// Determine base URL and path prefix based on request path
	isSSEPath := c.Request.URL.Path == "/sse/openapi.json"

	// For /sse/openapi.json requests, use empty base URL (relative paths)
	// For /openapi.json requests, use full base URL
	baseURL := h.baseURL
	if isSSEPath {
		baseURL = "" // Empty base for relative paths
	}

	spec, err := GenerateSpec(baseURL, h.mcpServer)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to generate OpenAPI spec")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to generate OpenAPI specification",
		})
		return
	}

	// Add tool execution endpoints
	tools := map[string]map[string]interface{}{
		"scrape_url": {
			"name":        "scrape_url",
			"description": "Fast HTTP scraper for static websites, blogs, news, documentation",
			"parameters": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to scrape (required)",
					"required":    true,
				},
				"timeout": map[string]interface{}{
					"type":        "integer",
					"description": "Timeout in seconds (default: 30)",
					"default":     30,
				},
				"user_agent": map[string]interface{}{
					"type":        "string",
					"description": "Custom User-Agent header",
				},
				"headers": map[string]interface{}{
					"type":        "object",
					"description": "Custom HTTP headers",
				},
			},
		},
		"scrape_with_js": {
			"name":        "scrape_with_js",
			"description": "Chrome-based scraper for dynamic websites with JavaScript rendering, GitHub, SPA, dashboards",
			"parameters": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to scrape (required)",
					"required":    true,
				},
				"timeout": map[string]interface{}{
					"type":        "integer",
					"description": "Timeout in seconds (default: 60)",
					"default":     60,
				},
				"output_format": map[string]interface{}{
					"type":        "string",
					"description": "Output format",
					"default":     "html",
					"enum":        []string{"html", "markdown"},
				},
				"screenshot_mode": map[string]interface{}{
					"type":        "string",
					"description": "When to take screenshot",
					"default":     "auto",
					"enum":        []string{"auto", "always", "never"},
				},
				"wait_for_network_idle": map[string]interface{}{
					"type":        "boolean",
					"description": "Wait for network idle (smart loading for SPA)",
					"default":     false,
				},
			},
		},
		"search_web": {
			"name":        "search_web",
			"description": "Web search using DuckDuckGo, Brave, or Bing",
			"parameters": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query (required)",
					"required":    true,
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results (default: 10)",
					"default":     10,
				},
			},
		},
		"parse_html": {
			"name":        "parse_html",
			"description": "Parse and extract structured data from HTML content",
			"parameters": map[string]interface{}{
				"html": map[string]interface{}{
					"type":        "string",
					"description": "HTML content to parse (required)",
					"required":    true,
				},
				"extract": map[string]interface{}{
					"type":        "array",
					"description": "CSS selectors to extract",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
		"smart_extract": {
			"name":        "smart_extract",
			"description": "Smart content extraction using LLM-based analysis of page structure",
			"parameters": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to extract from (required)",
					"required":    true,
				},
				"tech": map[string]interface{}{
					"type":        "string",
					"description": "Technology stack (auto-detected if empty)",
				},
			},
		},
	}

	// For /sse/openapi.json requests, add only /tools paths (relative)
	// For /openapi.json requests, add both /tools and /sse/tools paths
	for toolName, toolInfo := range tools {
		if isSSEPath {
			// For /sse/openapi.json - use relative paths (no /sse prefix in paths)
			AddToolEndpoint(spec, toolName, toolInfo["description"].(string), toolInfo["parameters"].(map[string]interface{}))
		} else {
			// For /openapi.json - add both variants
			AddToolEndpoint(spec, toolName, toolInfo["description"].(string), toolInfo["parameters"].(map[string]interface{}))
			AddToolEndpoint(spec, toolName, toolInfo["description"].(string), toolInfo["parameters"].(map[string]interface{}), "/sse")
		}
	}

	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, spec)
}

// ListTools returns all available tools
func (h *Handler) ListTools(c *gin.Context) {
	tools := []map[string]interface{}{
		{
			"name":        "scrape_url",
			"description": "Fast HTTP scraper for static websites",
			"parameters": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to scrape",
					"required":    true,
				},
				"timeout": map[string]interface{}{
					"type":        "integer",
					"description": "Timeout in seconds",
					"default":     30,
				},
				"user_agent": map[string]interface{}{
					"type":        "string",
					"description": "Custom User-Agent",
				},
			},
		},
		{
			"name":        "scrape_with_js",
			"description": "Chrome-based scraper for dynamic websites with JavaScript rendering",
			"parameters": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to scrape",
					"required":    true,
				},
				"timeout": map[string]interface{}{
					"type":        "integer",
					"description": "Timeout in seconds",
					"default":     60,
				},
				"output_format": map[string]interface{}{
					"type":        "string",
					"description": "Output format (html or markdown)",
					"default":     "html",
					"enum":        []string{"html", "markdown"},
				},
				"screenshot_mode": map[string]interface{}{
					"type":        "string",
					"description": "When to take screenshot",
					"default":     "auto",
					"enum":        []string{"auto", "always", "never"},
				},
			},
		},
		{
			"name":        "search_web",
			"description": "Web search using DuckDuckGo, Brave, or Bing",
			"parameters": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query",
					"required":    true,
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results",
					"default":     10,
				},
			},
		},
		{
			"name":        "parse_html",
			"description": "Parse and extract structured data from HTML",
			"parameters": map[string]interface{}{
				"html": map[string]interface{}{
					"type":        "string",
					"description": "HTML content to parse",
					"required":    true,
				},
				"extract": map[string]interface{}{
					"type":        "array",
					"description": "List of elements to extract",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
	}

	c.JSON(http.StatusOK, tools)
}

// ExecuteTool executes an MCP tool via REST API
func (h *Handler) ExecuteTool(c *gin.Context) {
	toolName := c.Param("tool")

	// Parse request body - try both formats
	var requestData map[string]interface{}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		h.logger.Error().Err(err).Str("tool", toolName).Msg("Failed to parse request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Extract arguments - support both formats:
	// 1. {"arguments": {...}} - MCP format
	// 2. {"url": "...", "timeout": 30} - REST format
	var arguments map[string]interface{}
	if args, ok := requestData["arguments"].(map[string]interface{}); ok {
		arguments = args
	} else {
		// Use entire request as arguments (except "arguments" key)
		arguments = make(map[string]interface{})
		for k, v := range requestData {
			if k != "arguments" {
				arguments[k] = v
			}
		}
	}

	// Create JSON-RPC request for MCP server
	mcpRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	}

	// Convert to JSON
	requestBytes, err := json.Marshal(mcpRequest)
	if err != nil {
		h.logger.Error().Err(err).Str("tool", toolName).Msg("Failed to marshal MCP request")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to process request",
		})
		return
	}

	// Execute via MCP server
	responseBytes, err := h.mcpServer.HandleMessage(c.Request.Context(), requestBytes)
	if err != nil {
		h.logger.Error().Err(err).Str("tool", toolName).Msg("Failed to execute tool")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Tool execution failed",
			"details": err.Error(),
		})
		return
	}

	// Parse MCP response
	var mcpResponse struct {
		Result map[string]interface{} `json:"result"`
		Error  *struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Data    interface{} `json:"data,omitempty"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(responseBytes, &mcpResponse); err != nil {
		h.logger.Error().Err(err).Str("tool", toolName).Msg("Failed to parse MCP response")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to process tool response",
		})
		return
	}

	// Check for MCP errors
	if mcpResponse.Error != nil {
		statusCode := http.StatusInternalServerError
		if mcpResponse.Error.Code == -32602 {
			statusCode = http.StatusBadRequest // Invalid params
		}
		c.JSON(statusCode, gin.H{
			"error": mcpResponse.Error.Message,
			"code": mcpResponse.Error.Code,
			"details": mcpResponse.Error.Data,
		})
		return
	}

	// Return successful result
	c.JSON(http.StatusOK, mcpResponse.Result)
}

// RegisterRoutes registers OpenAPI/REST routes
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// OpenAPI specification
	router.GET("/openapi.json", h.GetOpenAPI)
	router.GET("/sse/openapi.json", h.GetOpenAPI) // For Open WebUI compatibility

	// Tools endpoints (main paths)
	api := router.Group("/tools")
	{
		api.GET("", h.ListTools)
		api.POST("/:tool", h.ExecuteTool)
	}

	// Tools endpoints (SSE compatibility paths for Open WebUI)
	sseTools := router.Group("/sse/tools")
	{
		sseTools.GET("", h.ListTools)
		sseTools.POST("/:tool", h.ExecuteTool)
	}
}
