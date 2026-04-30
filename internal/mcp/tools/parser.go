package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type ParseHTMLTool struct {
	*BaseTool
	logger zerolog.Logger
}

func NewParseHTMLTool() *ParseHTMLTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"html": map[string]interface{}{
				"type":        "string",
				"description": "HTML content to parse",
			},
			"selector": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector to extract elements (e.g., '.class', '#id', 'div')",
			},
			"extract": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"text", "html", "attr", "all"},
				"description": "What to extract: text content, inner HTML, specific attribute, or all",
				"default":     "text",
			},
			"attribute": map[string]interface{}{
				"type":        "string",
				"description": "Attribute name to extract (required when extract='attr')",
			},
			"metadata": map[string]interface{}{
				"type":        "boolean",
				"description": "Include metadata like element count, positions",
				"default":     false,
			},
		},
		"required": []string{"html"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &ParseHTMLTool{
			logger: logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	return &ParseHTMLTool{
		BaseTool: NewBaseTool(
			"parse_html",
			"Parses HTML content and extracts elements using CSS selectors",
			schema,
			handler,
		),
	}
}

func (t *ParseHTMLTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Extract HTML
	html, ok := args["html"].(string)
	if !ok || html == "" {
		return nil, fmt.Errorf("html is required and must be a non-empty string")
	}

	// Extract selector
	selector, ok := args["selector"].(string)
	if !ok || selector == "" {
		selector = "*" // Match all elements if no selector provided
	}

	// Extract what to extract
	extract := "text"
	if e, ok := args["extract"].(string); ok && e != "" {
		extract = e
	}

	// Extract attribute name if needed
	attribute := ""
	if extract == "attr" {
		attribute, ok = args["attribute"].(string)
		if !ok || attribute == "" {
			return nil, fmt.Errorf("attribute is required when extract='attr'")
		}
	}

	// Extract metadata flag
	metadata := false
	if m, ok := args["metadata"].(bool); ok {
		metadata = m
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Find elements
	doc.Find("script, style").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	elements := doc.Find(selector)
	if elements.Length() == 0 {
		return map[string]interface{}{
			"selector":       selector,
			"elements_found": 0,
			"results":        []interface{}{},
			"message":        "No elements found matching the selector",
		}, nil
	}

	// Extract data from elements
	var results []interface{}
	elements.Each(func(i int, s *goquery.Selection) {
		var result map[string]interface{}

		switch extract {
		case "text":
			result = map[string]interface{}{
				"index": i,
				"text":  strings.TrimSpace(s.Text()),
			}
		case "html":
			html, err := s.Html()
			if err == nil {
				result = map[string]interface{}{
					"index": i,
					"html":  strings.TrimSpace(html),
				}
			}
		case "attr":
			attrValue, exists := s.Attr(attribute)
			if exists {
				result = map[string]interface{}{
					"index":     i,
					"attribute": attribute,
					"value":     attrValue,
				}
			}
		case "all":
			text := strings.TrimSpace(s.Text())
			htmlContent, _ := s.Html()

			result = map[string]interface{}{
				"index": i,
				"text":  text,
				"html":  strings.TrimSpace(htmlContent),
				"tag":   goquery.NodeName(s),
			}

			// Extract common attributes
			attrs := make(map[string]string)
			if href, exists := s.Attr("href"); exists {
				attrs["href"] = href
			}
			if src, exists := s.Attr("src"); exists {
				attrs["src"] = src
			}
			if id, exists := s.Attr("id"); exists {
				attrs["id"] = id
			}
			if class, exists := s.Attr("class"); exists {
				attrs["class"] = class
			}
			if len(attrs) > 0 {
				result["attributes"] = attrs
			}
		}

		if result != nil {
			results = append(results, result)
		}
	})

	// Build response
	response := map[string]interface{}{
		"selector":       selector,
		"elements_found": len(results),
		"extract":        extract,
		"results":        results,
	}

	// Add metadata if requested
	if metadata {
		metaData := map[string]interface{}{
			"total_elements": elements.Length(),
			"selector_type":  t.guessSelectorType(selector),
		}

		// Add document info
		if title := doc.Find("title").Text(); title != "" {
			metaData["page_title"] = strings.TrimSpace(title)
		}

		response["metadata"] = metaData
	}

	t.logger.Info().
		Str("selector", selector).
		Str("extract", extract).
		Int("elements_found", len(results)).
		Msg("HTML parsing completed")

	return response, nil
}

func (t *ParseHTMLTool) guessSelectorType(selector string) string {
	switch {
	case strings.HasPrefix(selector, "#"):
		return "id"
	case strings.HasPrefix(selector, "."):
		return "class"
	case strings.Contains(selector, "["):
		return "attribute"
	case strings.Contains(selector, ">"):
		return "child"
	case strings.Contains(selector, "+"):
		return "adjacent"
	case strings.Contains(selector, "~"):
		return "sibling"
	case strings.Contains(selector, " "):
		return "descendant"
	default:
		return "tag"
	}
}
