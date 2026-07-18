package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-shiori/go-readability"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

type SmartExtractorTool struct {
	*BaseTool
	logger     zerolog.Logger
	cache      *cache.Cache
	uaRotator  *useragent.Rotator
	proxy      *proxy.Rotator
	httpScraper *HTTPScraper
}

func NewSmartExtractorTool(cache *cache.Cache, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *SmartExtractorTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"html": map[string]interface{}{
				"type":        "string",
				"description": "HTML content to extract from. Provide this OR 'url'. Use 'html' when the page was already fetched by scrape_url/scrape_with_js to avoid a second download.",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"format":      "uri",
				"description": "URL to fetch and extract from (the tool downloads it for you). Provide this OR 'html'.",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Extraction mode. 'general' (default) and 'clean_text' use readability to pull the main article. 'news/tech/finance/legal/medical' apply domain-specific regex heuristics. 'links' extracts hyperlinks. 'catalog' discovers and extracts e-commerce products with pagination.",
				"enum":        []string{"news", "tech", "finance", "legal", "medical", "general", "clean_text", "links", "catalog"},
				"default":     "general",
			},
			"max_pages": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum catalog pages to discover (for catalog mode)",
				"default":     3,
			},
			"max_items": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum items to extract (for structured modes)",
				"default":     10,
			},
		},
		"anyOf": []map[string]interface{}{
			{"required": []string{"html"}},
			{"required": []string{"url"}},
		},
		"additionalProperties": false,
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &SmartExtractorTool{
			logger:      logger.Get(),
			cache:       cache,
			uaRotator:   uaRotator,
			proxy:       proxy,
			httpScraper: NewHTTPScraper(cache, uaRotator, proxy),
		}
		return tool.execute(ctx, args)
	}

	return &SmartExtractorTool{
		BaseTool: NewBaseTool(
			"smart_extract",
			"Extract the meaningful content from a web page, discarding navigation/ads/boilerplate to save tokens. Accepts either a 'url' (the tool fetches it) or 'html' (already-fetched content). For most article-style pages, the default 'general' mode uses readability and is the right choice; 'clean_text' returns plain paragraphs only. Use 'smart_extract' instead of reading raw HTML when you need to answer a question about a page's content.",
			schema,
			handler,
		),
	}
}

func (t *SmartExtractorTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Log what parameters were received for debugging
	t.logger.Debug().
		Interface("received_args", args).
		Interface("arg_keys", getMapKeys(args)).
		Msg("smart_extract called with args")

	// Validate input parameters
	if args == nil {
		return nil, fmt.Errorf("smart_extract requires either 'html' or 'url' parameter. Example: smart_extract(url=\"https://example.com\", mode=\"general\")")
	}

	// Check mode first - catalog mode needs URL for discovery
	mode := "general"
	if m, ok := args["mode"].(string); ok {
		mode = m
	}

	// Special handling for catalog mode - pass URL directly
	if mode == "catalog" {
		if urlStr, ok := args["url"].(string); ok {
			t.logger.Info().Str("url", urlStr).Str("mode", mode).Msg("Catalog mode detected, passing URL directly")
			result := t.extractCatalog(ctx, urlStr, args)
			return BuildMCPResponse(map[string]interface{}{
				"mode":   mode,
				"result": result,
			}, nil)
		} else if htmlVal, ok := args["html"].(string); ok {
			// Fallback: extract from provided HTML only
			t.logger.Info().Str("mode", mode).Msg("Catalog mode with HTML, extracting products from provided content")
			result := t.extractProductsFromHTML(htmlVal)
			return BuildMCPResponse(map[string]interface{}{
				"mode":   mode,
				"result": result,
			}, nil)
		} else {
			return nil, fmt.Errorf("catalog mode requires either 'url' or 'html' parameter")
		}
	}

	// For other modes, process as before
	var html string
	var err error

	// Check if url parameter is provided
	if urlStr, ok := args["url"].(string); ok {
		t.logger.Info().Str("url", urlStr).Str("mode", mode).Msg("smart_extract called with url parameter, will scrape first")

		// Use the unified scraper to get content
		html, err = t.scrapeURL(ctx, urlStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scrape URL: %w", err)
		}

		t.logger.Info().
			Str("url", urlStr).
			Int("html_length", len(html)).
			Msg("Successfully scraped URL for smart_extract")
	} else if htmlVal, ok := args["html"].(string); ok {
		// HTML parameter provided directly
		html = htmlVal
	} else {
		// Neither url nor html provided
		if len(args) == 0 {
			return nil, fmt.Errorf("smart_extract requires either 'html' or 'url' parameter. Example: smart_extract(url=\"https://example.com\", mode=\"general\") or smart_extract(html=\"<html>content</html>\", mode=\"general\")")
		}

		// Log what we received instead
		t.logger.Warn().
			Interface("received_args", args).
			Msg("smart_extract called without 'html' or 'url' parameter")

		return nil, fmt.Errorf("smart_extract requires either 'html' or 'url' parameter - received: %v. Example: smart_extract(url=\"https://example.com\", mode=\"general\")", getMapKeys(args))
	}

	// Validate html is not empty
	if strings.TrimSpace(html) == "" {
		return nil, fmt.Errorf("smart_extract 'html' parameter cannot be empty. Please provide the HTML content you want to extract information from")
	}

	maxItems := 10
	if items, ok := args["max_items"].(float64); ok {
		maxItems = int(items)
	}

	// Optional URL — needed for readability to resolve relative links
	urlStr := ""
	if u, ok := args["url"].(string); ok {
		urlStr = u
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
		result = t.extractCleanText(html, urlStr)
	case "links":
		result = t.extractLinks(html)
	default:
		result = t.extractGeneral(html, urlStr)
	}

	return BuildMCPResponse(map[string]interface{}{
		"mode":   mode,
		"result": result,
	}, nil)
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
	apiRegex := regexp.MustCompile(`(?i)((?:GET|POST|PUT|DELETE|PATCH|query|mutation))\s+["']?(/[^\s"']+)["']?`)
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

	// Extract document structure (Go regex doesn't support backreferences)
	headingsRegex := regexp.MustCompile(`<(h[1-6])[^>]*>(.+?)</h[1-6]>`)
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

// extractCleanText extracts the main article content via readability.
// Falls back to regex-based text cleaning if readability can't parse the page
// (e.g. minimal HTML fragments, non-article pages).
func (t *SmartExtractorTool) extractCleanText(html, pageURL string) map[string]interface{} {
	if article, ok := t.parseReadability(html, pageURL); ok {
		paragraphs := htmlToParagraphs(article.Content)
		text := strings.Join(paragraphs, "\n\n")
		return map[string]interface{}{
			"type":        "clean_text",
			"engine":      "readability",
			"title":       article.Title,
			"text":        text,
			"paragraphs":  paragraphs,
			"word_count":  countWords(text),
			"excerpt":     article.Excerpt,
			"byline":      article.Byline,
			"site_name":   article.SiteName,
			"language":    article.Language,
		}
	}

	// Fallback: legacy regex-based cleaning
	t.logger.Debug().Msg("readability failed for clean_text, falling back to regex")
	text := t.cleanHTML(html)
	text = strings.Join(strings.Fields(text), ". ")

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
		"engine":      "regex",
		"paragraphs":  cleanParas,
		"word_count":  countWords(text),
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

// extractGeneral extracts the main article content with metadata via readability.
// Falls back to regex-based extraction if readability can't parse the page.
func (t *SmartExtractorTool) extractGeneral(html, pageURL string) map[string]interface{} {
	if article, ok := t.parseReadability(html, pageURL); ok {
		paragraphs := htmlToParagraphs(article.Content)
		text := strings.Join(paragraphs, "\n\n")
		metadata := map[string]string{
			"excerpt":   article.Excerpt,
			"byline":    article.Byline,
			"site_name": article.SiteName,
			"language":  article.Language,
		}
		if article.Image != "" {
			metadata["image"] = article.Image
		}
		if article.PublishedTime != nil {
			metadata["published_time"] = article.PublishedTime.Format("2006-01-02T15:04:05Z")
		}
		return map[string]interface{}{
			"type":        "general",
			"engine":      "readability",
			"title":       article.Title,
			"metadata":    metadata,
			"text":        text,
			"paragraphs":  paragraphs,
			"excerpt":     article.Excerpt,
			"word_count":  countWords(text),
			"html":        article.Content, // readability-cleaned HTML
		}
	}

	// Fallback: legacy regex-based extraction
	t.logger.Debug().Msg("readability failed for general, falling back to regex")
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
		"engine":      "regex",
		"title":       title,
		"metadata":    metadata,
		"paragraphs":  paragraphs,
		"word_count":  countWords(t.cleanHTML(html)),
	}
}

// parseReadability runs go-readability on the HTML, returning the parsed article.
// pageURL is optional (used to resolve relative links). Returns ok=false if
// readability can't extract meaningful content — caller should fall back.
func (t *SmartExtractorTool) parseReadability(htmlStr, pageURL string) (readability.Article, bool) {
	var pageURLPtr *url.URL
	if pageURL != "" {
		if u, err := url.Parse(pageURL); err == nil && u.Host != "" {
			pageURLPtr = u
		}
	}

	article, err := readability.FromReader(strings.NewReader(htmlStr), pageURLPtr)
	if err != nil {
		t.logger.Debug().Err(err).Msg("readability parse error")
		return readability.Article{}, false
	}
	// Heuristic: if readability extracted < 200 chars, treat as failure
	if len(strings.TrimSpace(article.TextContent)) < 200 {
		t.logger.Debug().Int("len", len(article.TextContent)).Msg("readability returned too-short content")
		return readability.Article{}, false
	}
	return article, true
}

// htmlToParagraphs extracts paragraphs from readability-cleaned HTML.
// Splits on block-level elements (<p>, <h1-6>, <li>, <br><br>), strips tags,
// collapses whitespace. Empty blocks are dropped.
func htmlToParagraphs(htmlStr string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		// Fallback: naive regex split if goquery fails
		return splitParagraphs(strings.TrimSpace(stripTags(htmlStr)))
	}

	var out []string
	doc.Find("p, h1, h2, h3, h4, h5, h6, li").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			out = append(out, text)
		}
	})

	// If we got nothing meaningful, fall back to plain text
	if len(out) == 0 {
		return splitParagraphs(strings.TrimSpace(stripTags(htmlStr)))
	}
	return out
}

// stripTags removes all HTML tags from s.
func stripTags(s string) string {
	return regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
}

// splitParagraphs splits text content into non-empty trimmed paragraphs.
func splitParagraphs(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// countWords returns the word count of s.
func countWords(s string) int {
	return len(strings.Fields(s))
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
		"«", "«",
		"»", "»",
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

// getMapKeys extracts keys from a map for debugging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// scrapeURL performs HTTP scraping to fetch HTML content using existing infrastructure
func (t *SmartExtractorTool) scrapeURL(ctx context.Context, urlStr string) (string, error) {
	t.logger.Info().Str("url", urlStr).Msg("smart_extract scraping URL")

	// Use HTTP scraper with caching and proper headers
	result, err := t.httpScraper.Scrape(ctx, urlStr, Options{
		OutputFormat: "html", // Get raw HTML for extraction
	})

	if err != nil {
		return "", fmt.Errorf("HTTP scraping failed: %w", err)
	}

	t.logger.Info().
		Str("url", urlStr).
		Int("html_size", len(result.HTML)).
		Str("method", result.Method).
		Bool("from_cache", result.FromCache).
		Msg("smart_extract successfully scraped URL")

	return result.HTML, nil
}

// extractCatalog discovers and extracts products from catalog pages
func (t *SmartExtractorTool) extractCatalog(ctx context.Context, url string, args map[string]interface{}) map[string]interface{} {
	t.logger.Info().Str("url", url).Msg("Starting catalog extraction with discovery")

	// Create catalog discovery service
	catalogDiscovery := NewCatalogDiscovery(t.cache, t.httpScraper)

	// Set configurable pagination depth
	maxPages := 3 // default
	if pages, ok := args["max_pages"].(float64); ok {
		maxPages = int(pages)
		t.logger.Info().Int("max_pages", maxPages).Msg("Using custom max_pages setting")
	}
	catalogDiscovery.SetMaxPages(maxPages)

	// Discover catalog pages using multi-strategy approach
	catalogPages, err := catalogDiscovery.DiscoverCatalogPages(ctx, url)
	if err != nil {
		t.logger.Error().Err(err).Str("url", url).Msg("Catalog discovery failed")
		return map[string]interface{}{
			"type":    "catalog",
			"error":   err.Error(),
			"products": []Product{},
		}
	}

	if len(catalogPages) == 0 {
		t.logger.Warn().Str("url", url).Msg("No catalog pages discovered, falling back to single page extraction")
		// Fallback: extract from the URL provided
		html, err := t.scrapeURL(ctx, url)
		if err != nil {
			return map[string]interface{}{
				"type":    "catalog",
				"error":   err.Error(),
				"products": []Product{},
			}
		}
		return t.extractProductsFromHTML(html)
	}

	t.logger.Info().
		Str("url", url).
		Int("catalog_pages", len(catalogPages)).
		Msg("Successfully discovered catalog pages")

	// Extract products from all discovered catalog pages
	var allProducts []Product
	catalogInfo := []map[string]interface{}{}

	for _, catalogPage := range catalogPages {
		t.logger.Info().
			Str("catalog_url", catalogPage.URL).
			Str("discovery_method", catalogPage.DiscoveryMethod).
			Msg("Extracting products from catalog page")

		// Scrape the catalog page
		pageHTML, err := t.scrapeURL(ctx, catalogPage.URL)
		if err != nil {
			t.logger.Warn().
				Err(err).
				Str("catalog_url", catalogPage.URL).
				Msg("Failed to scrape catalog page")
			continue
		}

		// Extract products from this page
		t.logger.Info().
			Str("catalog_url", catalogPage.URL).
			Int("page_html_length", len(pageHTML)).
			Msg("Extracting products from catalog page HTML")

		products := catalogDiscovery.ExtractProducts(pageHTML)

		t.logger.Info().
			Str("catalog_url", catalogPage.URL).
			Int("products_found", len(products)).
			Msg("Product extraction completed")

		allProducts = append(allProducts, products...)

		// Track catalog info
		catalogInfo = append(catalogInfo, map[string]interface{}{
			"url":             catalogPage.URL,
			"title":           catalogPage.Title,
			"product_count":   len(products),
			"discovery_method": catalogPage.DiscoveryMethod,
			"has_pagination":  catalogPage.HasPagination,
		})

		t.logger.Debug().
			Str("catalog_url", catalogPage.URL).
			Int("products_found", len(products)).
			Msg("Successfully extracted products from catalog page")
	}

	// Remove duplicates based on product name
	uniqueProducts := t.deduplicateProducts(allProducts)

	t.logger.Info().
		Str("url", url).
		Int("total_products", len(allProducts)).
		Int("unique_products", len(uniqueProducts)).
		Int("pages_analyzed", len(catalogPages)).
		Msg("Catalog extraction completed")

	return map[string]interface{}{
		"type":           "catalog",
		"products":       uniqueProducts,
		"total_products": len(uniqueProducts),
		"pages_analyzed": len(catalogPages),
		"catalog_info":   catalogInfo,
	}
}

// extractProductsFromHTML extracts products from provided HTML without catalog discovery
func (t *SmartExtractorTool) extractProductsFromHTML(html string) map[string]interface{} {
	t.logger.Debug().Msg("Extracting products from provided HTML")

	catalogDiscovery := NewCatalogDiscovery(t.cache, t.httpScraper)
	products := catalogDiscovery.ExtractProducts(html)

	// Remove duplicates
	uniqueProducts := t.deduplicateProducts(products)

	return map[string]interface{}{
		"type":           "catalog",
		"products":       uniqueProducts,
		"total_products": len(uniqueProducts),
		"pages_analyzed": 1,
		"source":         "provided_html",
	}
}

// deduplicateProducts removes duplicate products based on name
func (t *SmartExtractorTool) deduplicateProducts(products []Product) []Product {
	seen := make(map[string]bool)
	var unique []Product

	for _, product := range products {
		normalized := strings.ToLower(strings.TrimSpace(product.Name))
		if normalized == "" {
			continue
		}

		if !seen[normalized] {
			seen[normalized] = true
			unique = append(unique, product)
		}
	}

	return unique
}
