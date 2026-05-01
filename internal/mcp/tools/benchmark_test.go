package tools

import (
	"testing"
)

func BenchmarkGetCacheKey(b *testing.B) {
	tool := &ScrapeTool{}

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
		tool.getCacheKey(url, map[string]interface{}{})
	}
}

func BenchmarkGetCacheKeyWithHeaders(b *testing.B) {
	tool := &ScrapeTool{}

	args := map[string]interface{}{
		"headers": map[string]interface{}{
			"Authorization": "Bearer token123",
			"X-Custom-Header": "value",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tool.getCacheKey("https://example.com", args)
	}
}

func BenchmarkExtractTitle(b *testing.B) {
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
		extractTitle(html)
	}
}
