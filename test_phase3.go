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
	baseURL := "http://localhost:8192"

	// Test sites for Phase 3 verification
	testSites := []struct {
		name string
		url  string
	}{
		{"pixelscan.net", "https://pixelscan.net"},
		{"bot.sannysoft.com", "https://bot.sannysoft.com"},
		{"arh.antoinevastel.com (fingerprint)", "https://arh.antoinevastel.com/bots/areyouheadless"},
		{"Cloudflare test", "https://nowsecure.com"},
	}

	fmt.Println("🧪 Phase 3 Extended Stealth Testing")
	fmt.Println("====================================")

	for _, site := range testSites {
		fmt.Printf("\n🌐 Testing: %s\n", site.name)
		fmt.Printf("URL: %s\n", site.url)

		// Create scrape request
		reqBody := map[string]interface{}{
			"url": site.url,
			"options": map[string]interface{}{
				"output_format":   "html",
				"stealth_enabled": true,
				"stealth_scroll":  true,
				"stealth_mouse":   false,
				"wait_for_duration": "3s",
			},
		}

		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			fmt.Printf("❌ Failed to marshal request: %v\n", err)
			continue
		}

		// Make request
		resp, err := http.Post(baseURL+"/mcp/tools/scrape_with_js", "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Printf("❌ Request failed: %v\n", err)
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("❌ Failed to read response: %v\n", err)
			continue
		}

		// Parse response
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			fmt.Printf("❌ Failed to parse response: %v\n", err)
			fmt.Printf("Raw response: %s\n", string(body))
			continue
		}

		// Check for errors
		if errorObj, ok := result["error"].(map[string]interface{}); ok {
			fmt.Printf("❌ Error: %v\n", errorObj["message"])
			continue
		}

		// Extract result data
		content := result["content"].(map[string]interface{})
		scrapeResult := content["result"].(map[string]interface{})

		title := scrapeResult["title"]
		sizeBytes := int(scrapeResult["size_bytes"].(float64))
		duration := scrapeResult["duration"].(map[string]interface{})
		durationMs := int(duration["ms"].(float64))

		fmt.Printf("✅ Success\n")
		fmt.Printf("   Title: %v\n", title)
		fmt.Printf("   Size: %d bytes\n", sizeBytes)
		fmt.Printf("   Duration: %d ms\n", durationMs)
		fmt.Printf("   Method: %v\n", scrapeResult["method"])

		// Check if from cache
		if fromCache, ok := scrapeResult["from_cache"].(bool); ok && fromCache {
			fmt.Printf("   ⚠️  From cache (not a real test)\n")
		}

		// Small delay between requests
		time.Sleep(1 * time.Second)
	}

	fmt.Println("\n====================================")
	fmt.Println("📊 Testing complete!")
	fmt.Println("\n💡 Next steps:")
	fmt.Println("1. Manually check pixelscan.net for webdriver property")
	fmt.Println("2. Manually check bot.sannysoft.com for stealth score")
	fmt.Println("3. Verify timezone/locale randomization in results")
}
