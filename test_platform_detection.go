package main

import (
	"fmt"
	"regexp"
	"strings"
)

func main() {
	urlStr := "https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp"

	// Test the exact code from chrome_scraper.go
	isGitHub := strings.Contains(urlStr, "github.com")
	isGitea := regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`).MatchString(urlStr)
	isGitLab := strings.Contains(urlStr, "gitlab.com") || regexp.MustCompile(`https://[^/]+/[^/]+/[^/]+/-/`).MatchString(urlStr)
	hasActions := false // assuming no actions for this test

	fmt.Printf("URL: %s\n", urlStr)
	fmt.Printf("isGitHub: %v\n", isGitHub)
	fmt.Printf("isGitea: %v\n", isGitea)
	fmt.Printf("isGitLab: %v\n", isGitLab)
	fmt.Printf("hasActions: %v\n", hasActions)
	fmt.Printf("Condition (isGitHub || isGitea || isGitLab) && !hasActions: %v\n", (isGitHub || isGitea || isGitLab) && !hasActions)
}