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
			"Intelligently extracts and structures content from HTML based on mode. Supports: news, tech (docs/API), finance (reports/data), legal (documents), medical (health info), general text cleaning, link extraction.",
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
	// Extract code blocks, API docs, technical content
	codeRegex := regexp.MustCompile(`<pre[^>]*><code[^>]*>(.+?)</code></pre>`)
	codeBlocks := codeRegex.FindAllStringSubmatch(html, -1)

	var codes []map[string]interface{}
	for _, match := range codeBlocks {
		code := t.cleanHTML(match[1])
		codes = append(codes, map[string]interface{}{
			"code":     code,
			"language": t.detectLanguage(code),
		})
	}

	// Extract headings
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
		"type":     "tech",
		"sections": sections,
		"codes":    codes,
	}
}

func (t *SmartExtractorTool) extractFinance(html string) map[string]interface{} {
	// Extract financial data, tables, numbers
	tableRegex := regexp.MustCompile(`<table[^>]*>(.+?)</table>`)
	tables := tableRegex.FindAllStringSubmatch(html, -1)

	var financialData []map[string]interface{}

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

// Helper functions from extract_news.go
func (t *SmartExtractorTool) extractNewsFromHTML(html string, maxItems int) []map[string]interface{} {
	// [Previous extractNewsFromHTML implementation]
	return []map[string]interface{}{}
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
