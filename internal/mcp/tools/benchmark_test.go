package tools

import (
	"testing"
)

func BenchmarkGenerateCacheKey(b *testing.B) {
	urls := []string{
		"https://example.com",
		"https://example.com/page1",
		"https://example.com/page2",
		"https://google.com",
		"https://github.com",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		url := urls[i%len(urls)]
		GenerateCacheKey(url, map[string]interface{}{})
	}
}

func BenchmarkGenerateCacheKeyWithParams(b *testing.B) {
	params := map[string]interface{}{
		"user_agent": "CustomAgent",
		"timeout":    "30s",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateCacheKey("https://example.com", params)
	}
}

func BenchmarkExtractTitleFromHTML(b *testing.B) {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>Test Page Title Here</title>
</head>
<body>
    <h1>Hello World</h1>
</body>
</html>`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractTitleFromHTML(html)
	}
}