package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MCP Tool Request Structure
type MCPRequest struct {
	URL            string `json:"url"`
	StealthEnabled bool   `json:"stealth_enabled"`
	StealthScroll  bool   `json:"stealth_scroll"`
	StealthMouse   bool   `json:"stealth_mouse"`
	OutputFormat   string `json:"output_format"`
}

// MCP Tool Response Structure
type MCPResponse struct {
	Success     bool   `json:"success"`
	HTML        string `json:"html,omitempty"`
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	FinalURL    string `json:"final_url,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	SizeBytes   int    `json:"size_bytes,omitempty"`
	Duration    string `json:"duration,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Method      string `json:"method,omitempty"`
	Error       string `json:"error,omitempty"`
}

func main() {
	fmt.Println("🧪 MCP Direct Testing")
	fmt.Println("=====================")
	fmt.Println()

	baseURL := "http://localhost:8192/tools/scrape_with_js"

	// Test scenarios
	scenarios := []struct {
		name string
		url  string
	}{
		{
			name: "Basic Site",
			url:  "https://example.com",
		},
		{
			name: "Bot Detection Test",
			url:  "https://bot.sannysoft.com",
		},
		{
			name: "Are You Headless",
			url:  "https://arh.antoinevastel.com/bots/areyouheadless",
		},
	}

	for i, scenario := range scenarios {
		fmt.Printf("🧪 Test %d/%d: %s\n", i+1, len(scenarios), scenario.name)
		fmt.Printf("   URL: %s\n", scenario.url)

		// Create request
		req := MCPRequest{
			URL:            scenario.url,
			StealthEnabled: true,
			StealthScroll:  true,
			StealthMouse:   false,
			OutputFormat:   "html",
		}

		jsonData, err := json.Marshal(req)
		if err != nil {
			fmt.Printf("   ❌ Failed to marshal request: %v\n", err)
			continue
		}

		// Send request
		startTime := time.Now()

		resp, err := http.Post(baseURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Printf("   ❌ Request failed: %v\n", err)
			fmt.Printf("   ℹ️  Make sure MCP server is running: ./bin/mcp-web-scrape\n")
			continue
		}

		duration := time.Since(startTime)

		// Read response
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			fmt.Printf("   ❌ Failed to read response: %v\n", err)
			continue
		}

		// Parse response
		var mcpResp MCPResponse
		if err := json.Unmarshal(body, &mcpResp); err != nil {
			fmt.Printf("   ⚠️  Could not parse response as MCP format\n")
			fmt.Printf("   📄 Raw response (first 500 chars): %s\n", string(body[:min(500, len(body))]))
		} else {
			// Display parsed response
			if mcpResp.Error != "" {
				fmt.Printf("   ❌ Error: %s\n", mcpResp.Error)
			} else {
				fmt.Printf("   ✅ Success!\n")
				fmt.Printf("   📄 HTML size: %d bytes\n", mcpResp.SizeBytes)
				fmt.Printf("   ⏱️  Duration: %s\n", duration)
				fmt.Printf("   🔧 Method: %s\n", mcpResp.Method)
				if mcpResp.Title != "" {
					fmt.Printf("   📝 Title: %s\n", mcpResp.Title)
				}
			}
		}

		fmt.Printf("   📊 HTTP Status: %d\n", resp.StatusCode)
		fmt.Printf("   ⏱️  Total time: %v\n", duration)
		fmt.Println()

		// Don't overwhelm the server
		time.Sleep(1 * time.Second)
	}

	fmt.Println("=====================")
	fmt.Println("✅ Testing Complete!")
	fmt.Println()
	fmt.Println("💡 Tips:")
	fmt.Println("   - Check server logs: tail -f /tmp/mcp-web-scrape.log")
	fmt.Println("   - Look for Phase 3-5 activation in logs")
	fmt.Println("   - Monitor success rate and performance")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
