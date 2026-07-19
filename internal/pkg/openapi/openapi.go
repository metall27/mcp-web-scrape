package openapi

import (
	"encoding/json"

	"github.com/metall/mcp-web-scrape/internal/mcp"
)

// Spec represents the OpenAPI 3.0 specification
type Spec struct {
	OpenAPI    string                 `json:"openapi"`
	Info       Info                   `json:"info"`
	Servers    []Server               `json:"servers"`
	Paths      map[string]PathItem    `json:"paths"`
	Components map[string]interface{} `json:"components,omitempty"`
}

type Info struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type Server struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type PathItem struct {
	Post   *Operation `json:"post,omitempty"`
	Get    *Operation `json:"get,omitempty"`
	Put    *Operation `json:"put,omitempty"`
	Delete *Operation `json:"delete,omitempty"`
}

type Operation struct {
	Summary     string              `json:"summary"`
	Description string              `json:"description,omitempty"`
	OperationID string              `json:"operationId"`
	Tags        []string            `json:"tags"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
}

type RequestBody struct {
	Content  map[string]MediaType `json:"content"`
	Required bool                 `json:"required"`
}

type MediaType struct {
	Schema map[string]interface{} `json:"schema"`
}

type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

type Parameter struct {
	Name        string                 `json:"name"`
	In          string                 `json:"in"`
	Description string                 `json:"description,omitempty"`
	Required    bool                   `json:"required"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
}

// GenerateSpec creates OpenAPI specification from MCP tools
func GenerateSpec(baseURL string, mcpServer *mcp.Server) (*Spec, error) {
	spec := &Spec{
		OpenAPI: "3.0.0",
		Info: Info{
			Title:       "MCP Web Scrape Server",
			Description: "REST API wrapper for MCP web scraping tools",
			Version:     "1.0.0",
		},
		Servers: []Server{
			{
				URL:         baseURL,
				Description: "MCP Web Scrape Server",
			},
		},
		Paths: make(map[string]PathItem),
		Components: map[string]interface{}{
			"schemas": map[string]interface{}{
				"ToolResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"content": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type": map[string]interface{}{
										"type": "string",
									},
									"text": map[string]interface{}{
										"type": "string",
									},
									"data": map[string]interface{}{
										"type":        "string",
										"description": "Base64 encoded data for images/screenshots",
									},
								},
							},
						},
						"isError": map[string]interface{}{
							"type": "boolean",
						},
					},
				},
				"Error": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"code": map[string]interface{}{
							"type": "integer",
						},
						"message": map[string]interface{}{
							"type": "string",
						},
						"details": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
	}

	// Add health endpoint
	spec.Paths["/health"] = PathItem{
		Get: &Operation{
			Summary:     "Health check",
			Description: "Check if the server is running",
			OperationID: "health",
			Tags:        []string{"Health"},
			Responses: map[string]Response{
				"200": {
					Description: "Server is healthy",
					Content: map[string]MediaType{
						"application/json": {
							Schema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"status": map[string]interface{}{
										"type": "string",
									},
									"time": map[string]interface{}{
										"type": "integer",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Add tools list endpoint
	spec.Paths["/tools"] = PathItem{
		Get: &Operation{
			Summary:     "List available tools",
			Description: "Get list of all available MCP tools",
			OperationID: "listTools",
			Tags:        []string{"Tools"},
			Responses: map[string]Response{
				"200": {
					Description: "List of tools",
					Content: map[string]MediaType{
						"application/json": {
							Schema: map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"name": map[string]interface{}{
											"type": "string",
										},
										"description": map[string]interface{}{
											"type": "string",
										},
										"inputSchema": map[string]interface{}{
											"type": "object",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Generate REST endpoints for each MCP tool from registered tools
	if mcpServer != nil {
		toolMap := mcpServer.GetTools()
		for _, toolName := range mcpServer.GetToolsOrder() {
			tool := toolMap[toolName]
			schema := tool.InputSchema()
			AddToolEndpointFromSchema(spec, toolName, tool.Description(), schema)
			// Also add /sse variant
			AddToolEndpointFromSchema(spec, toolName, tool.Description(), schema, "/sse")
		}
	}

	return spec, nil
}

// GenerateJSON returns the JSON representation of the OpenAPI spec
func (s *Spec) GenerateJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// AddToolEndpointFromSchema adds a REST endpoint using a full JSON Schema object
// (type, properties, required, additionalProperties — as returned by Tool.InputSchema())
func AddToolEndpointFromSchema(spec *Spec, toolName, toolDescription string, fullSchema map[string]interface{}, prefixes ...string) {
	// Default prefix if not provided
	prefix := ""
	if len(prefixes) > 0 {
		prefix = prefixes[0]
	}

	path := prefix + "/tools/" + toolName

	// Extract properties and required from the full schema
	properties, ok := fullSchema["properties"].(map[string]interface{})
	if !ok {
		properties = make(map[string]interface{})
	}

	requiredList, _ := fullSchema["required"].([]string)

	requestSchema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(requiredList) > 0 {
		requestSchema["required"] = requiredList
	}

	spec.Paths[path] = PathItem{
		Post: &Operation{
			Summary:     toolDescription,
			Description: toolDescription,
			OperationID: "execute_" + toolName,
			Tags:        []string{"Tools"},
			RequestBody: &RequestBody{
				Content: map[string]MediaType{
					"application/json": {
						Schema: requestSchema,
					},
				},
				Required: true,
			},
			Responses: map[string]Response{
				"200": {
					Description: "Tool execution result",
					Content: map[string]MediaType{
						"application/json": {
							Schema: map[string]interface{}{
								"$ref": "#/components/schemas/ToolResponse",
							},
						},
					},
				},
				"400": {
					Description: "Invalid request",
					Content: map[string]MediaType{
						"application/json": {
							Schema: map[string]interface{}{
								"$ref": "#/components/schemas/Error",
							},
						},
					},
				},
				"500": {
					Description: "Tool execution failed",
					Content: map[string]MediaType{
						"application/json": {
							Schema: map[string]interface{}{
								"$ref": "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
	}
}
