package main

import (
	"fmt"
	"regexp"
)

func convertGiteaURL(urlStr string) string {
	if regexp.MustCompile(`gitea\.[^/]+|gitea\.(com|io)`).MatchString(urlStr) {
		// Extract base domain for self-hosted Gitea instances
		baseDomain := "gitea.com"
		if matches := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlStr); len(matches) > 0 {
			if matches[1] != "gitea.com" && matches[1] != "gitea.io" {
				baseDomain = matches[1]
			}
		}

		// Extract path without domain
		path := urlStr
		if matches := regexp.MustCompile(`https://[^/]+(/.*)`).FindStringSubmatch(urlStr); len(matches) > 0 {
			path = matches[1]
		}

		// IMPORTANT: Check specific patterns FIRST before generic repo pattern
		// Gitea commits page → API
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/commits/branch/([^/]+)`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			branch := matches[3]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/commits?sha=%s&limit=10", baseDomain, owner, repo, branch)
		}

		// Gitea commits page (simple) → API
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/commits/?$`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/commits?limit=10", baseDomain, owner, repo)
		}

		// Gitea commit page → API
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/commit/([a-f0-9]+)`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			sha := matches[3]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s/git/commits/%s", baseDomain, owner, repo, sha)
		}

		// Gitea repo page → API (check LAST, as it's most generic)
		if matches := regexp.MustCompile(`/([^/]+)/([^/]+)/?$`).FindStringSubmatch(path); len(matches) > 0 {
			owner := matches[1]
			repo := matches[2]
			return fmt.Sprintf("https://%s/api/v1/repos/%s/%s", baseDomain, owner, repo)
		}
	}

	return urlStr
}

func main() {
	urls := []string{
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/commits/branch/master",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/commits/",
		"https://gitea.0x27.ru/huggingface/ggml-org_llama.cpp/commit/b8275a8",
	}

	for _, url := range urls {
		result := convertGiteaURL(url)
		fmt.Printf("Original: %s\n", url)
		fmt.Printf("Result:   %s\n\n", result)
	}
}