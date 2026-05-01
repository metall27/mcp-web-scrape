package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type ExtractNewsTool struct {
	*BaseTool
	logger zerolog.Logger
}

func NewExtractNewsTool() *ExtractNewsTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"html": map[string]interface{}{
				"type":        "string",
				"description": "HTML content to extract news from",
			},
			"max_items": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of news items to extract (default: 10)",
				"default":     10,
			},
		},
		"required": []string{"html"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &ExtractNewsTool{
			logger: logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	return &ExtractNewsTool{
		BaseTool: NewBaseTool(
			"extract_news",
			"Extracts news headlines and links from HTML content. Optimized for Russian news sites like mail.ru, lenta.ru, rbc.ru.",
			schema,
			handler,
		),
	}
}

func (t *ExtractNewsTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	html, ok := args["html"].(string)
	if !ok {
		return nil, fmt.Errorf("html is required and must be a string")
	}

	maxItems := 10
	if items, ok := args["max_items"].(float64); ok {
		maxItems = int(items)
	}

	// Extract news items
	news := t.extractNewsFromHTML(html, maxItems)

	return map[string]interface{}{
		"count": len(news),
		"news":  news,
	}, nil
}

func (t *ExtractNewsTool) extractNewsFromHTML(html string, maxItems int) []map[string]interface{} {
	var news []map[string]interface{}

	// Try multiple patterns for Russian news sites
	patterns := []struct {
		name   string
		title  string
		link   string
		time   string
	}{
		// Mail.ru pattern
		{
			title:  `<a class="list__text"[^>]*>(.+?)</a>`,
			link:   `href="([^"]+)"`,
			time:   `<span class="list__time">(.+?)</span>`,
		},
		// Generic news patterns
		{
			title:  `<h[23][^>]*><a[^>]*>(.+?)</a></h[23]>`,
			link:   `href="([^"]+)"`,
			time:   `<time[^>]*>(.+?)</time>`,
		},
		{
			title:  `<a[^>]*class="[^"]*title[^"]*"[^>]*>(.+?)</a>`,
			link:   `href="([^"]+)"`,
		},
	}

	for _, pattern := range patterns {
		titleRegex := regexp.MustCompile(pattern.title)
		linkRegex := regexp.MustCompile(pattern.link)

		titleMatches := titleRegex.FindAllStringSubmatch(html, -1)
		linkMatches := linkRegex.FindAllStringSubmatch(html, -1)

		for i := 0; i < len(titleMatches) && i < maxItems; i++ {
			if i >= len(linkMatches) {
				break
			}

			title := strings.TrimSpace(titleMatches[i][1])
			link := strings.TrimSpace(linkMatches[i][1])

			// Clean up HTML entities
			title = t.cleanHTML(title)
			title = t.decodeEntities(title)

			if title == "" || link == "" {
				continue
			}

			// Make absolute URL if relative
			if strings.HasPrefix(link, "/") {
				link = "https://news.mail.ru" + link
			} else if !strings.HasPrefix(link, "http") {
				link = "https://" + link
			}

			news = append(news, map[string]interface{}{
				"title": title,
				"link":  link,
			})

			if len(news) >= maxItems {
				break
			}
		}

		if len(news) > 0 {
			break
		}
	}

	return news
}

func (t *ExtractNewsTool) cleanHTML(text string) string {
	// Remove HTML tags
	re := regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, " ")

	// Clean up whitespace
	text = strings.Join(strings.Fields(text), " ")

	return text
}

func (t *ExtractNewsTool) decodeEntities(text string) string {
	// Simple HTML entity decoding
	replacer := strings.NewReplacer(
		"&quot;", "\"",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&nbsp;", " ",
		"&#39;", "'",
		"&mdash;", "—",
		"&ndash;", "–",
	)
	return replacer.Replace(text)
}
