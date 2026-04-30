package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	serverURL := "http://localhost:8080/mcp"

	// Example 1: Initialize connection
	fmt.Println("=== Testing MCP Server ===\n")

	if err := testHealth(serverURL); err != nil {
		fmt.Printf("Health check failed: %v\n", err)
		return
	}

	// Example 2: List available tools
	fmt.Println("=== Available Tools ===\n")
	tools, err := listTools(serverURL)
	if err != nil {
		fmt.Printf("Failed to list tools: %v\n", err)
		return
	}
	for _, tool := range tools {
		fmt.Printf("- %s: %s\n", tool["name"], tool["description"])
	}

	// Example 3: Scrape a URL
	fmt.Println("\n=== Scraping Example ===\n")
	scrapeResult, err := callTool(serverURL, "scrape_url", map[string]interface{}{
		"url": "https://example.com",
	})
	if err != nil {
		fmt.Printf("Scrape failed: %v\n", err)
	} else {
		fmt.Printf("Scraped: %s\n", scrapeResult["url"])
		fmt.Printf("Status: %d\n", scrapeResult["status_code"])
		fmt.Printf("Size: %d bytes\n", scrapeResult["size_bytes"])
		if title, ok := scrapeResult["title"].(string); ok {
			fmt.Printf("Title: %s\n", title)
		}
	}

	// Example 4: Parse HTML
	fmt.Println("\n=== HTML Parsing Example ===\n")
	htmlContent := `<html>
<body>
    <h1>Hello World</h1>
    <a href="https://example.com" class="link">Example Link</a>
    <a href="https://google.com" class="link">Google</a>
</body>
</html>`

	parseResult, err := callTool(serverURL, "parse_html", map[string]interface{}{
		"html":     htmlContent,
		"selector": "a.link",
		"extract":  "attr",
		"attribute": "href",
		"metadata": true,
	})
	if err != nil {
		fmt.Printf("Parse failed: %v\n", err)
	} else {
		fmt.Printf("Found %d elements\n", parseResult["elements_found"])
		if results, ok := parseResult["results"].([]interface{}); ok {
			for _, r := range results {
				if result, ok := r.(map[string]interface{}); ok {
					fmt.Printf("- %s\n", result["value"])
				}
			}
		}
	}

	// Example 5: Search
	fmt.Println("\n=== Search Example ===\n")
	searchResult, err := callTool(serverURL, "search_web", map[string]interface{}{
		"query":       "golang web scraping",
		"max_results": 3,
	})
	if err != nil {
		fmt.Printf("Search failed: %v\n", err)
	} else {
		fmt.Printf("Query: %s\n", searchResult["query"])
		fmt.Printf("Provider: %s\n", searchResult["provider"])
		fmt.Printf("Results: %d\n", searchResult["total_count"])
	}

	fmt.Println("\n=== Tests Complete ===")
}

func testHealth(serverURL string) error {
	healthURL := "http://localhost:8080/health"
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(healthURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}

func listTools(serverURL string) ([]map[string]interface{}, error) {
	request := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	response, err := sendRequest(serverURL, request)
	if err != nil {
		return nil, err
	}

	result := response.Result.(map[string]interface{})
	tools := result["tools"].([]interface{})

	var toolList []map[string]interface{}
	for _, tool := range tools {
		toolList = append(toolList, tool.(map[string]interface{}))
	}

	return toolList, nil
}

func callTool(serverURL string, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	request := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}

	response, err := sendRequest(serverURL, request)
	if err != nil {
		return nil, err
	}

	if response.Error != nil {
		return nil, fmt.Errorf("tool error: %s", response.Error.Message)
	}

	return response.Result.(map[string]interface{}), nil
}

func sendRequest(serverURL string, request MCPRequest) (*MCPResponse, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	resp, err := client.Post(serverURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response MCPResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, err
	}

	return &response, nil
}

type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
