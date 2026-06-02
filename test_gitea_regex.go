package main

import (
	"fmt"
	"regexp"
	"strings"
)

func main() {
	// Test the Gitea detection patterns
	urls := []string{
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/commits/branch/master",
		"https://gitea.com/some/repo",
	}

	pattern1 := regexp.MustCompile(`gitea\.[^/]+`)
	pattern2 := regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/(issues|pulls|releases|commit)`)
	pattern3 := regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/(issues|pulls|releases|commits|commit)`)

	for _, url := range urls {
		fmt.Printf("\nURL: %s\n", url)
		fmt.Printf("  Pattern 'gitea.[^/]+' matches: %v\n", pattern1.MatchString(url))
		fmt.Printf("  Pattern 2 matches: %v\n", pattern2.MatchString(url))
		fmt.Printf("  Pattern 3 matches: %v\n", pattern3.MatchString(url))

		// Test detection logic
		isGitea := strings.Contains(url, "gitea.com") || strings.Contains(url, "gitea.io") ||
			pattern1.MatchString(url) || pattern3.MatchString(url)
		fmt.Printf("  Would be detected as Gitea: %v\n", isGitea)
	}
}