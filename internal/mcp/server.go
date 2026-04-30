package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/mcp/tools"
	"github.com/rs/zerolog"
	"golang.org/x/time/rate"
)

type Server struct {
	config       Config
	logger       zerolog.Logger
	cache        *cache.Cache
	rateLimiter  *rate.Limiter
	tools        map[string]tools.Tool
	serverInfo   ServerInfo
}

type Config struct {
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	RateLimit       RateLimitConfig
	Cache           *cache.Cache
}

type RateLimitConfig struct {
	RequestsPerSecond float64
	BurstSize         int
	Enabled           bool
}

func New(cfg Config) (*Server, error) {
	s := &Server{
		config:     cfg,
		logger:     logger.Get(),
		cache:      cfg.Cache,
		serverInfo: ServerInfo{Name: cfg.ServerName, Version: cfg.ServerVersion},
		tools:      make(map[string]tools.Tool),
	}

	// Setup rate limiter
	if cfg.RateLimit.Enabled {
		s.rateLimiter = rate.NewLimiter(
			rate.Limit(cfg.RateLimit.RequestsPerSecond),
			cfg.RateLimit.BurstSize,
		)
	}

	// Register default tools
	if err := s.registerDefaultTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	return s, nil
}

func (s *Server) registerDefaultTools() error {
	defaultTools := []tools.Tool{
		tools.NewScrapeTool(),
		tools.NewScrapeJSTool(),
		tools.NewSearchTool(),
		tools.NewParseHTMLTool(),
	}

	for _, tool := range defaultTools {
		if err := s.RegisterTool(tool); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", tool.Name(), err)
		}
	}

	return nil
}

func (s *Server) RegisterTool(tool tools.Tool) error {
	s.tools[tool.Name()] = tool
	s.logger.Info().
		Str("tool", tool.Name()).
		Msg("Tool registered")
	return nil
}

// HandleMessage processes an MCP message and returns the response
func (s *Server) HandleMessage(ctx context.Context, data []byte) ([]byte, error) {
	// Parse request
	var req JSONRPCMessage
	if err := json.Unmarshal(data, &req); err != nil {
		return s.buildError(nil, -32700, "Parse error", nil)
	}

	// Validate JSON-RPC version
	if req.JSONRPC != "2.0" {
		return s.buildError(req.ID, -32600, "Invalid Request", "Only JSON-RPC 2.0 supported")
	}

	// Check rate limit
	if s.rateLimiter != nil {
		if !s.rateLimiter.Allow() {
			return s.buildError(req.ID, -32000, "Rate limit exceeded", nil)
		}
	}

	// Route to handler
	switch req.Method {
	case "initialize":
		return s.handleInitialize(ctx, &req)
	case "tools/list":
		return s.handleToolsList(ctx, &req)
	case "tools/call":
		return s.handleToolCall(ctx, &req)
	case "resources/list":
		return s.handleResourcesList(ctx, &req)
	case "prompts/list":
		return s.handlePromptsList(ctx, &req)
	case "ping":
		return s.handlePing(ctx, &req)
	default:
		return s.buildError(req.ID, -32601, "Method not found", fmt.Sprintf("Method '%s' not found", req.Method))
	}
}

func (s *Server) handleInitialize(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
	result := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{
				ListChanged: false,
			},
		},
		ServerInfo: s.serverInfo,
	}

	return s.buildSuccess(req.ID, result)
}

func (s *Server) handleToolsList(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
	toolsList := []Tool{}

	for _, tool := range s.tools {
		toolsList = append(toolsList, Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.InputSchema(),
		})
	}

	result := map[string]interface{}{
		"tools": toolsList,
	}

	return s.buildSuccess(req.ID, result)
}

func (s *Server) handleToolCall(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
	// Parse params
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments,omitempty"`
	}

	if req.Params != nil {
		paramsBytes, _ := json.Marshal(req.Params)
		if err := json.Unmarshal(paramsBytes, &params); err != nil {
			return s.buildError(req.ID, -32602, "Invalid params", "Failed to parse tool call params")
		}
	}

	// Find tool
	tool, exists := s.tools[params.Name]
	if !exists {
		return s.buildError(req.ID, -32602, "Tool not found", fmt.Sprintf("Tool '%s' not found", params.Name))
	}

	// Execute tool
	result, err := tool.Execute(ctx, params.Arguments)
	if err != nil {
		s.logger.Error().
			Str("tool", params.Name).
			Err(err).
			Msg("Tool execution failed")
		return s.buildError(req.ID, -32000, "Tool execution failed", err.Error())
	}

	return s.buildSuccess(req.ID, result)
}

func (s *Server) handleResourcesList(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
	result := map[string]interface{}{
		"resources": []Resource{},
	}

	return s.buildSuccess(req.ID, result)
}

func (s *Server) handlePromptsList(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
	result := map[string]interface{}{
		"prompts": []Prompt{},
	}

	return s.buildSuccess(req.ID, result)
}

func (s *Server) handlePing(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
	return s.buildSuccess(req.ID, map[string]interface{}{})
}

func (s *Server) buildSuccess(id interface{}, result interface{}) ([]byte, error) {
	response := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	return json.Marshal(response)
}

func (s *Server) buildError(id interface{}, code int, message string, data interface{}) ([]byte, error) {
	response := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}

	return json.Marshal(response)
}

// GetCapabilities returns server capabilities
func (s *Server) GetCapabilities() ServerCapabilities {
	return ServerCapabilities{
		Tools: &ToolsCapability{
			ListChanged: false,
		},
	}
}

// GetServerInfo returns server information
func (s *Server) GetServerInfo() ServerInfo {
	return s.serverInfo
}

// GenerateSessionID generates a unique session ID
func GenerateSessionID() string {
	return uuid.New().String()
}

// GetRateLimitInfo returns current rate limit info
func (s *Server) GetRateLimitInfo() RateLimitInfo {
	if s.rateLimiter == nil {
		return RateLimitInfo{}
	}

	return RateLimitInfo{
		RequestsPerSecond: s.config.RateLimit.RequestsPerSecond,
		BurstSize:         s.config.RateLimit.BurstSize,
		ResetAt:          time.Now(),
	}
}
