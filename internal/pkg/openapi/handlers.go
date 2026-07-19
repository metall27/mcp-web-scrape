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

	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, spec)
}

// ListTools returns all available tools (from MCP server)
func (h *Handler) ListTools(c *gin.Context) {
	toolMap := h.mcpServer.GetTools()
	toolsList := make([]map[string]interface{}, 0, len(toolMap))

	for _, toolName := range h.mcpServer.GetToolsOrder() {
		tool := toolMap[toolName]
		schema := tool.InputSchema()

		// Extract properties for a cleaner REST representation
		properties, _ := schema["properties"].(map[string]interface{})
		requiredList, _ := schema["required"].([]string)

		// Build a simple parameters map from the schema properties
		params := make(map[string]interface{})
		requiredSet := make(map[string]bool)
		for _, r := range requiredList {
			requiredSet[r] = true
		}

		for name, info := range properties {
			if infoMap, ok := info.(map[string]interface{}); ok {
				param := map[string]interface{}{
					"type": infoMap["type"],
				}
				if desc, ok := infoMap["description"].(string); ok {
					param["description"] = desc
				}
				if def, ok := infoMap["default"]; ok {
					param["default"] = def
				}
				if enum, ok := infoMap["enum"]; ok {
					param["enum"] = enum
				}
				if requiredSet[name] {
					param["required"] = true
				}
				params[name] = param
			}
		}

		toolsList = append(toolsList, map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  params,
		})
	}

	c.JSON(http.StatusOK, toolsList)
}

// ExecuteTool executes an MCP tool via REST API
func (h *Handler) ExecuteTool(c *gin.Context) {
	toolName := c.Param("tool")

	// Parse request body - try both formats
	var requestData map[string]interface{}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		h.logger.Error().Err(err).Str("tool", toolName).Msg("Failed to parse request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
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
			"error":   "Tool execution failed",
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
			"error":   mcpResponse.Error.Message,
			"code":    mcpResponse.Error.Code,
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
