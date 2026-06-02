package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	// Test direct HTTP requests to GitHub
	testURLs := []string{
		"https://api.github.com/repos/open-webui/open-webui/releases",
		"https://github.com/open-webui/open-webui/releases.atom",
		"https://raw.githubusercontent.com/open-webui/open-webui/main/README.md",
	}

	fmt.Printf("🧪 Testing GitHub HTTP endpoints directly\n\n")

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	for i, testURL := range testURLs {
		fmt.Printf("Test %d: %s\n", i+1, testURL)

		req, err := http.NewRequest("GET", testURL, nil)
		if err != nil {
			fmt.Printf("❌ Failed to create request: %v\n\n", err)
			continue
		}

		// Set headers
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json,application/vnd.github.v3+json")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("❌ Request failed: %v\n\n", err)
			continue
		}
		defer resp.Body.Close()

		fmt.Printf("   Status: %d %s\n", resp.StatusCode, resp.Status)
		fmt.Printf("   Content-Type: %s\n", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("❌ Failed to read body: %v\n\n", err)
			continue
		}

		fmt.Printf("   Size: %d bytes\n", len(body))

		// Show preview
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		fmt.Printf("   Preview: %s\n\n", preview)

		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("🎉 GitHub HTTP endpoints test complete!\n")
}
