package main

import (
	"fmt"
	"regexp"
)

func main() {
	// Test with query parameters
	urls := []string{
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/?test_new_1780409212",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/?abc=xyz",
	}

	// Exact regex from the code
	pattern := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`)

	fmt.Println("Testing Gitea detection with query parameters:")
	for _, url := range urls {
		isGitea := pattern.MatchString(url)
		fmt.Printf("URL: %s\n", url)
		fmt.Printf("  Match: %v\n\n", isGitea)
	}
}