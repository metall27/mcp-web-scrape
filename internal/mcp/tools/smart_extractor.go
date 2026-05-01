package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type SmartExtractorTool struct {
	*BaseTool
	logger zerolog.Logger
}

func NewSmartExtractorTool() *SmartExtractorTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"html": map[string]interface{}{
				"type":        "string",
				"description": "HTML content to extract from",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Extraction mode: news, tech, finance, legal, medical, general, clean_text, links",
				"enum":        []string{"news", "tech", "finance", "legal", "medical", "general", "clean_text", "links"},
				"default":     "general",
			},
			"max_items": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum items to extract (for structured modes)",
				"default":     10,
			},
		},
		"required": []string{"html"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &SmartExtractorTool{
			logger: logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	return &SmartExtractorTool{
		BaseTool: NewBaseTool(
			"smart_extract",
			"Intelligently extracts and structures content from HTML. Use AFTER scrape_with_js to get structured data. Modes: news=headlines, tech=API/docs, finance=reports, legal=docs, medical=health, clean_text=plain text, links=all URLs. Converts raw HTML into clean, structured data.",
			schema,
			handler,
		),
	}
}

func (t *SmartExtractorTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	html, ok := args["html"].(string)
	if !ok {
		return nil, fmt.Errorf("html is required and must be a string")
	}

	mode := "general"
	if m, ok := args["mode"].(string); ok {
		mode = m
	}

	maxItems := 10
	if items, ok := args["max_items"].(float64); ok {
		maxItems = int(items)
	}

	var result interface{}

	switch mode {
	case "news":
		result = t.extractNews(html, maxItems)
	case "tech":
		result = t.extractTech(html)
	case "finance":
		result = t.extractFinance(html)
	case "legal":
		result = t.extractLegal(html)
	case "medical":
		result = t.extractMedical(html)
	case "clean_text":
		result = t.extractCleanText(html)
	case "links":
		result = t.extractLinks(html)
	default:
		result = t.extractGeneral(html)
	}

	return map[string]interface{}{
		"mode":   mode,
		"result": result,
	}, nil
}

func (t *SmartExtractorTool) extractNews(html string, maxItems int) map[string]interface{} {
	news := t.extractNewsFromHTML(html, maxItems)
	return map[string]interface{}{
		"type":  "news",
		"count": len(news),
		"items": news,
	}
}

func (t *SmartExtractorTool) extractTech(html string) map[string]interface{} {
	// Extract code blocks with language detection
	codeRegex := regexp.MustCompile(`(?i)<pre[^>]*><code[^>]*class="(?:language-|highlight-)?([^"]*)"?[^>]*>(.+?)</code></pre>`)
	codeBlocks := codeRegex.FindAllStringSubmatch(html, -1)

	var codes []map[string]interface{}
	for _, match := range codeBlocks {
		var language string
		if len(match) > 2 && match[1] != "" {
			language = match[1]
		} else {
			language = t.detectLanguage(match[2])
		}
		code := t.cleanHTML(match[2])
		codes = append(codes, map[string]interface{}{
			"code":     code,
			"language": language,
		})
	}

	// Extract API endpoints (REST/GraphQL)
	apiRegex := regexp.MustCompile(`(?i)(?:GET|POST|PUT|DELETE|PATCH|query|mutation)\s+["']?(/[^\s"']+)["']?`)
	apiMatches := apiRegex.FindAllStringSubmatch(html, -1)

	var endpoints []map[string]string
	for _, match := range apiMatches {
		if len(match) > 2 {
			endpoints = append(endpoints, map[string]string{
				"method": match[1],
				"path":   match[2],
			})
		}
	}

	// Extract command line examples
	cliRegex := regexp.MustCompile(`(?i)(?:`+"```"+`\s*(?:bash|sh|shell|cmd)?|<code[^>]*class="language-(?:bash|sh)">)([^\n]+(?:\n[^\n]+)*?)(?:`+"```"+`|</code>)`)
	cliExamples := cliRegex.FindAllStringSubmatch(html, -1)

	var commands []string
	for _, match := range cliExamples {
		if len(match) > 1 {
			cmd := t.cleanHTML(match[1])
			if len(cmd) > 3 && len(cmd) < 500 {
				commands = append(commands, cmd)
			}
		}
	}

	// Extract configuration examples
	configRegex := regexp.MustCompile(`(?i)(?:config|configuration|settings|\.env|\.yaml|\.json|\.toml|\.ini)[^:]*[:\s]+([^\n<]+(?:\n[^\n<]+){0,5})`)
	configs := configRegex.FindAllStringSubmatch(html, -1)

	var configuration []string
	for _, match := range configs {
		if len(match) > 1 {
			config := strings.TrimSpace(match[1])
			if len(config) > 5 {
				configuration = append(configuration, config)
			}
		}
	}

	// Extract technical terms and keywords
	techTerms := []string{
		"API", "REST", "GraphQL", "WebSocket", "SDK", "library", "framework",
		"database", "SQL", "NoSQL", "cache", "queue", "microservices",
		"authentication", "authorization", "OAuth", "JWT", "token",
		"endpoint", "request", "response", "payload", "schema",
	}
	foundTerms := make(map[string]bool)
	lowerHTML := strings.ToLower(html)
	for _, term := range techTerms {
		if strings.Contains(lowerHTML, strings.ToLower(term)) {
			foundTerms[term] = true
		}
	}

	// Extract version numbers
	versionRegex := regexp.MustCompile(`(?:v|version)?\s*(\d+\.\d+(?:\.\d+)?)`)
	versions := versionRegex.FindAllString(html, 10)

	// Extract document structure
	headingsRegex := regexp.MustCompile(`<(h[1-6])[^>]*>(.+?)</\1>`)
	headings := headingsRegex.FindAllStringSubmatch(html, -1)

	var sections []map[string]interface{}
	for _, match := range headings {
		level := match[1]
		title := t.cleanHTML(match[2])
		sections = append(sections, map[string]interface{}{
			"level": level,
			"title": title,
		})
	}

	return map[string]interface{}{
		"type":         "tech",
		"sections":     sections,
		"codes":        codes,
		"endpoints":    endpoints,
		"commands":     commands[:min(len(commands), 10)],
		"configs":      configuration[:min(len(configuration), 10)],
		"terms":        foundTerms,
		"versions":     versions,
		"code_blocks":  len(codes),
		"api_count":    len(endpoints),
	}
}

func (t *SmartExtractorTool) extractFinance(html string) map[string]interface{} {
	// Extract financial data, tables, numbers
	tableRegex := regexp.MustCompile(`<table[^>]*>(.+?)</table>`)
	tables := tableRegex.FindAllStringSubmatch(html, -1)

	// Look for currency patterns
	currencyRegex := regexp.MustCompile(`[$€₽£]\s*[\d,]+(?:\.\d+)?|\d+[\d,]*(?:\.\d+)?\s*(?:USD|EUR|RUB|GBP)`)
	matches := currencyRegex.FindAllString(html, -1)

	return map[string]interface{}{
		"type":           "finance",
		"tables_count":   len(tables),
		"currency_mentions": len(matches),
		"amounts":        matches[:min(len(matches), 20)],
	}
}

func (t *SmartExtractorTool) extractLegal(html string) map[string]interface{} {
	// Extract legal structure: articles, paragraphs, references
	articleRegex := regexp.MustCompile(`(?:статья|article|§)\s*[\d.]+(?i)`)
	articles := articleRegex.FindAllString(html, -1)

	// Extract document structure
	structure := map[string]string{
		"has_articles":     fmt.Sprintf("%v", len(articles) > 0),
		"article_count":    fmt.Sprintf("%d", len(articles)),
		"document_length":  fmt.Sprintf("%d", len(html)),
	}

	return map[string]interface{}{
		"type":     "legal",
		"articles": articles,
		"structure": structure,
	}
}

func (t *SmartExtractorTool) extractMedical(html string) map[string]interface{} {
	// Extract medical/health information: symptoms, diagnoses, treatments, medications
	text := strings.ToLower(html)

	// Common medical terms patterns
	symptomsRegex := regexp.MustCompile(`(?:симптом|symptom|признак|sign|жалоба)[^.:]*[.:]`)
	symptoms := symptomsRegex.FindAllString(text, 20)

	diagnosisRegex := regexp.MustCompile(`(?:диагноз|diagnosis|заболевание|disease|болезнь)[^.:]*[.:]`)
	diagnoses := diagnosisRegex.FindAllString(text, 20)

	medicationRegex := regexp.MustCompile(`(?:препарат|drug|medication|лекарство|лекарствен|таблетка|мг|мл)[^.:]*[.:]`)
	medications := medicationRegex.FindAllString(text, 20)

	dosageRegex := regexp.MustCompile(`\d+\s*(?:мг|мл|mg|ml|г|g|таблеток|tablet|капель|drop|раз|times?)`)
	dosages := dosageRegex.FindAllString(html, 15)

	// Medical measurements
	vitalsRegex := regexp.MustCompile(`(?:давление|pressure|температура|temperature|пульс|pulse|частота|rate)[^.:]*[.:]`)
	vitals := vitalsRegex.FindAllString(text, 10)

	// Look for structured medical data sections
	sectionRegex := regexp.MustCompile(`<(?:h[2-3]|strong|b)[^>]*>(?:анамнез|history|осмотр|examination|назначен|prescribed|лечение|treatment)[^<]*</(?:h[2-3]|strong|b)>`)
	sections := sectionRegex.FindAllString(html, -1)

	return map[string]interface{}{
		"type":              "medical",
		"symptoms_count":    len(symptoms),
		"symptoms":          symptoms[:min(len(symptoms), 10)],
		"diagnoses_count":   len(diagnoses),
		"diagnoses":         diagnoses[:min(len(diagnoses), 10)],
		"medications_count": len(medications),
		"medications":       medications[:min(len(medications), 10)],
		"dosages":           dosages[:min(len(dosages), 10)],
		"vitals":            vitals[:min(len(vitals), 10)],
		"structured_sections": len(sections),
	}
}

func (t *SmartExtractorTool) extractCleanText(html string) map[string]interface{} {
	// Remove all HTML, clean whitespace
	text := t.cleanHTML(html)
	text = strings.Join(strings.Fields(text), ". ")

	// Split into paragraphs
	paragraphs := regexp.MustCompile(`\.\s+`).Split(text, -1)

	var cleanParas []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if len(p) > 50 { // Only meaningful paragraphs
			cleanParas = append(cleanParas, p)
		}
	}

	return map[string]interface{}{
		"type":        "clean_text",
		"paragraphs":  cleanParas,
		"word_count":  len(strings.Fields(text)),
	}
}

func (t *SmartExtractorTool) extractLinks(html string) map[string]interface{} {
	linkRegex := regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*>(.+?)</a>`)
	matches := linkRegex.FindAllStringSubmatch(html, -1)

	var links []map[string]interface{}
	for _, match := range matches {
		url := match[1]
		text := t.cleanHTML(match[2])

		if text == "" {
			text = "Link"
		}

		links = append(links, map[string]interface{}{
			"url":  url,
			"text": text,
		})
	}

	return map[string]interface{}{
		"type":  "links",
		"count": len(links),
		"links": links,
	}
}

func (t *SmartExtractorTool) extractGeneral(html string) map[string]interface{} {
	// General extraction: title, main content, metadata
	titleRegex := regexp.MustCompile(`<title[^>]*>(.+?)</title>`)
	titleMatch := titleRegex.FindStringSubmatch(html)

	title := ""
	if len(titleMatch) > 1 {
		title = t.cleanHTML(titleMatch[1])
	}

	metaRegex := regexp.MustCompile(`<meta[^>]*name="([^"]+)"[^>]*content="([^"]+)"`)
	metaMatches := metaRegex.FindAllStringSubmatch(html, -1)

	metadata := make(map[string]string)
	for _, match := range metaMatches {
		if len(match) > 2 {
			metadata[match[1]] = match[2]
		}
	}

	// Extract main text content (paragraphs)
	paraRegex := regexp.MustCompile(`<p[^>]*>(.+?)</p>`)
	paraMatches := paraRegex.FindAllStringSubmatch(html, -1)

	var paragraphs []string
	for _, match := range paraMatches {
		text := t.cleanHTML(match[1])
		if len(text) > 20 {
			paragraphs = append(paragraphs, text)
		}
	}

	return map[string]interface{}{
		"type":        "general",
		"title":       title,
		"metadata":    metadata,
		"paragraphs":  paragraphs,
		"word_count":  len(strings.Fields(t.cleanHTML(html))),
	}
}

// Helper functions
func (t *SmartExtractorTool) extractNewsFromHTML(html string, maxItems int) []map[string]interface{} {
	var news []map[string]interface{}

	// Multiple patterns for Russian news sites
	patterns := []struct {
		titlePattern string
		linkPattern  string
	}{
		// Mail.ru pattern
		{
			titlePattern: `<a[^>]*class="list__text"[^>]*>(.+?)</a>`,
			linkPattern:  `href="([^"]+)"`,
		},
		// Generic news patterns
		{
			titlePattern: `<h[23][^>]*><a[^>]*>(.+?)</a></h[23]>`,
			linkPattern:  `href="([^"]+)"`,
		},
		{
			titlePattern: `<a[^>]*class="[^"]*title[^"]*"[^>]*>(.+?)</a>`,
			linkPattern:  `href="([^"]+)"`,
		},
		{
			titlePattern: `<a[^>]*class="[^"]*headline[^"]*"[^>]*>(.+?)</a>`,
			linkPattern:  `href="([^"]+)"`,
		},
	}

	for _, pattern := range patterns {
		titleRegex := regexp.MustCompile(pattern.titlePattern)
		linkRegex := regexp.MustCompile(pattern.linkPattern)

		titleMatches := titleRegex.FindAllStringSubmatch(html, maxItems*2)
		linkMatches := linkRegex.FindAllStringSubmatch(html, maxItems*2)

		for i := 0; i < len(titleMatches) && i < maxItems; i++ {
			if i >= len(linkMatches) {
				break
			}

			title := t.cleanHTML(titleMatches[i][1])
			link := strings.TrimSpace(linkMatches[i][1])

			// Clean up HTML entities
			title = t.decodeEntities(title)

			if title == "" || link == "" {
				continue
			}

			// Make absolute URL if relative
			if strings.HasPrefix(link, "/") {
				// Try to detect base URL from context or use default
				if strings.Contains(html, "mail.ru") {
					link = "https://news.mail.ru" + link
				} else {
					link = "https://" + link
				}
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

func (t *SmartExtractorTool) decodeEntities(text string) string {
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
		"« ", "",
		" »", "",
	)
	return replacer.Replace(text)
}

func (t *SmartExtractorTool) cleanHTML(text string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

func (t *SmartExtractorTool) detectLanguage(code string) string {
	// Simple language detection
	if strings.Contains(code, "def ") || strings.Contains(code, "import ") {
		return "python"
	}
	if strings.Contains(code, "func ") || strings.Contains(code, "package ") {
		return "go"
	}
	if strings.Contains(code, "function ") || strings.Contains(code, "const ") {
		return "javascript"
	}
	return "unknown"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
