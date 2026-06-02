package main

import (
	"fmt"
	"regexp"
	"strings"
)

func main() {
	url := "https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/"

	// Test the regex patterns I used in the code
	pattern1 := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`)
	pattern2 := regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`)

	isGitea := pattern1.MatchString(url)
	isGitLab := strings.Contains(url, "gitlab.com") || pattern2.MatchString(url)

	fmt.Printf("URL: %s\n", url)
	fmt.Printf("isGitea pattern match: %v\n", isGitea)
	fmt.Printf("isGitLab pattern match: %v\n", isGitLab)

	// Test with different URLs
	urls := []string{
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/",
		"https://gitea.com/some/repo",
		"https://gitlab.com/some/repo",
	}

	fmt.Printf("\nTesting multiple URLs:\n")
	for _, url := range urls {
		isGitea := pattern1.MatchString(url)
		fmt.Printf("URL: %s -> isGitea: %v\n", url, isGitea)
	}
}