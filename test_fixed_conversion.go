package main

import (
	"fmt"
	"regexp"
)

func convertGiteaCommitsURL(urlStr string) string {
	// Extract base domain for self-hosted Gitea instances
	baseDomain := "gitea.com"
	if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
		if matches[1] != "gitea.com" && matches[1] != "gitea.io" {
			baseDomain = matches[1]
		}
	}

	// Gitea commits page → API (fixed version)
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/commits/branch/([^/]+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		branch := matches[3]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/commits?sha=%s&limit=10", baseDomain, owner, repo, branch)
	}

	return urlStr
}

func main() {
	url := "https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/commits/branch/master"
	converted := convertGiteaCommitsURL(url)
	fmt.Printf("Original: %s\n", url)
	fmt.Printf("Converted: %s\n", converted)

	// Test the converted URL directly
	fmt.Printf("\nLet's test this URL directly with curl:\n")
	fmt.Printf("URL: %s\n", converted)
}