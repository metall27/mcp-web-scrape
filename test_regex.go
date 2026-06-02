package main

import (
	"fmt"
	"regexp"
)

func main() {
	// Test the Gitea detection patterns
	url := "https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp"

	pattern1 := regexp.MustCompile(`gitea\.[^/]+`)
	fmt.Printf("Pattern 'gitea.[^/]+' matches: %v\n", pattern1.MatchString(url))

	pattern2 := regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/(issues|pulls|releases|commit)`)
	fmt.Printf("Pattern 'issues/pulls/releases/commit' matches: %v\n", pattern2.MatchString(url))

	// Test repo pattern
	pattern3 := regexp.MustCompile(`([^/]+)/([^/]+)/?$`)
	matches := pattern3.FindStringSubmatch("/huggingface/ggml-org_llama.cpp")
	if len(matches) > 0 {
		fmt.Printf("Repo pattern matches: %v, Owner: %s, Repo: %s\n", len(matches) > 3, matches[1], matches[2])
	}
}