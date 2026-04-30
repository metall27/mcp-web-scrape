package tools

import (
	"context"
)

// Tool represents an MCP tool
type Tool interface {
	// Name returns the unique name of the tool
	Name() string

	// Description returns a human-readable description
	Description() string

	// InputSchema returns the JSON Schema for input validation
	InputSchema() map[string]interface{}

	// Execute runs the tool with given arguments
	Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error)
}

// BaseTool provides common functionality for all tools
type BaseTool struct {
	name        string
	description string
	schema      map[string]interface{}
	handler     func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error)
}

func NewBaseTool(name, description string, schema map[string]interface{}, handler func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error)) *BaseTool {
	return &BaseTool{
		name:        name,
		description: description,
		schema:      schema,
		handler:     handler,
	}
}

func (t *BaseTool) Name() string {
	return t.name
}

func (t *BaseTool) Description() string {
	return t.description
}

func (t *BaseTool) InputSchema() map[string]interface{} {
	return t.schema
}

func (t *BaseTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	return t.handler(ctx, args)
}
