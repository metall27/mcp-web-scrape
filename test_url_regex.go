package main

import (
	"fmt"
	"regexp"
	"strings"
)

func main() {
	urls := []string{
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/?xyz=123",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/?test=abc",
	}

	pattern := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`)

	fmt.Println("Testing Gitea detection pattern:")
	for _, url := range urls {
		isGitea := pattern.MatchString(url)
		fmt.Printf("URL: %s\n", url)
		fmt.Printf("  Match: %v\n\n", isGitea)
	}

	// Test the actual logic from the code
	fmt.Println("\nTesting actual logic:")
	for _, url := range urls {
		isGitea := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`).MatchString(url)
		isGitLab := strings.Contains(url, "gitlab.com") || regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`).MatchString(url)
		isGitHub := strings.Contains(url, "github.com")

		fmt.Printf("URL: %s\n", url)
		fmt.Printf("  isGitHub: %v, isGitea: %v, isGitLab: %v\n", isGitHub, isGitea, isGitLab)
		fmt.Printf("  Would trigger API fallback: %v\n\n", (isGitHub || isGitea || isGitLab))
	}
}