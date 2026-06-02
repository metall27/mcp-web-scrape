package main

import (
	"fmt"
	"regexp"
)

func convertGiteaURL(urlStr string) string {
	// Extract base domain for self-hosted Gitea instances
	baseDomain := "gitea.com"
	if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
		if matches[1] != "gitea.com" && matches[1] != "gitea.io" {
			baseDomain = matches[1]
		}
	}

	// Gitea commit page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/commit/([a-f0-9]+)`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		sha := matches[3]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/git/commits/%s", baseDomain, owner, repo, sha)
	}

	// Gitea releases page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/releases`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/releases", baseDomain, owner, repo)
	}

	// Gitea issues page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/issues`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/issues", baseDomain, owner, repo)
	}

	// Gitea pulls page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/pulls`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/pulls", baseDomain, owner, repo)
	}

	// Gitea repo page → API
	if matches := regexp.MustCompile(`([^/]+)/([^/]+)/?$`).FindStringSubmatch(urlStr); len(matches) > 0 {
		owner := matches[1]
		repo := matches[2]
		return fmt.Sprintf("https://%s/api/v1/repos/%s/%s", baseDomain, owner, repo)
	}

	return urlStr
}

func main() {
	url := "https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp"
	converted := convertGiteaURL(url)
	fmt.Printf("Original URL: %s\n", url)
	fmt.Printf("Converted URL: %s\n", converted)
}