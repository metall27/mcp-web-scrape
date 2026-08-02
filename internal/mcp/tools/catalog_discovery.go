package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

// CatalogPage represents a discovered catalog page
type CatalogPage struct {
	URL             string
	Title           string
	ProductCount    int
	HasPagination   bool
	LastChecked     time.Time
	DiscoveryMethod string // "sitemap", "links", "patterns"
}

// Product represents a basic product structure
type Product struct {
	Name        string
	Price       string
	Description string
	Specs       map[string]string
	ImageURL    string
	ProductURL  string
}

// CatalogDiscovery handles intelligent catalog page discovery
type CatalogDiscovery struct {
	sitemapParser *SitemapParser
	httpScraper   *HTTPScraper
	logger        zerolog.Logger
	cache         *cache.Cache

	// Configurable parameters
	productThreshold int      // Minimum products to validate as catalog page
	maxPages         int      // Maximum pages to discover
	catalogPatterns  []string // Known catalog path patterns
}

// NewCatalogDiscovery creates a new catalog discovery instance
func NewCatalogDiscovery(cache *cache.Cache, httpScraper *HTTPScraper) *CatalogDiscovery {
	return &CatalogDiscovery{
		sitemapParser:    NewSitemapParser(cache, httpScraper),
		httpScraper:      httpScraper,
		cache:            cache,
		logger:           logger.Get(),
		productThreshold: 3, // Default: at least 3 products to be valid catalog
		maxPages:         3, // Default: discover up to 3 catalog pages
		catalogPatterns: []string{
			"/catalog/", "/shop/", "/products/", "/store/",
			"/goods/", "/product/", "/category/", "/categories/",
		},
	}
}

// SetProductThreshold sets the minimum product count threshold
func (c *CatalogDiscovery) SetProductThreshold(threshold int) {
	c.productThreshold = threshold
}

// SetMaxPages sets the maximum number of catalog pages to discover
func (c *CatalogDiscovery) SetMaxPages(maxPages int) {
	c.maxPages = maxPages
}

// DiscoverCatalogPages is the main discovery function with fallback strategies
func (c *CatalogDiscovery) DiscoverCatalogPages(ctx context.Context, baseURL string) ([]CatalogPage, error) {
	c.logger.Info().Str("base_url", baseURL).Msg("Starting catalog page discovery")

	// Strategy 1: Sitemap-based discovery (most reliable)
	if catalogPages := c.discoverFromSitemap(ctx, baseURL); len(catalogPages) > 0 {
		c.logger.Info().
			Str("method", "sitemap").
			Int("pages_found", len(catalogPages)).
			Msg("Catalog discovery successful via sitemap")
		return catalogPages, nil
	}

	// Strategy 2: Link-based discovery from homepage
	if catalogPages := c.discoverFromLinks(ctx, baseURL); len(catalogPages) > 0 {
		c.logger.Info().
			Str("method", "links").
			Int("pages_found", len(catalogPages)).
			Msg("Catalog discovery successful via link analysis")
		return catalogPages, nil
	}

	// Strategy 3: Common pattern detection
	if catalogPages := c.discoverFromPatterns(ctx, baseURL); len(catalogPages) > 0 {
		c.logger.Info().
			Str("method", "patterns").
			Int("pages_found", len(catalogPages)).
			Msg("Catalog discovery successful via pattern detection")
		return catalogPages, nil
	}

	c.logger.Warn().Str("base_url", baseURL).Msg("No catalog pages discovered")
	return []CatalogPage{}, nil
}

// discoverFromSitemap discovers catalog pages using sitemap.xml
func (c *CatalogDiscovery) discoverFromSitemap(ctx context.Context, baseURL string) []CatalogPage {
	// Discover sitemap URL
	sitemapURLs := c.sitemapParser.DiscoverSitemapURL(baseURL)
	if len(sitemapURLs) == 0 {
		c.logger.Debug().Str("base_url", baseURL).Msg("No sitemap found")
		return []CatalogPage{}
	}

	var allCatalogPages []CatalogPage

	// Parse all discovered sitemaps
	for _, sitemapURL := range sitemapURLs {
		entries, err := c.sitemapParser.ParseSitemap(ctx, sitemapURL)
		if err != nil {
			c.logger.Warn().
				Err(err).
				Str("sitemap_url", sitemapURL).
				Msg("Failed to parse sitemap")
			continue
		}

		// Find catalog-related URLs
		catalogURLs := c.sitemapParser.FindCatalogURLs(entries)

		// Validate each catalog page
		for _, catalogURL := range catalogURLs {
			if c.validateCatalogPage(ctx, catalogURL) {
				page := CatalogPage{
					URL:             catalogURL,
					DiscoveryMethod: "sitemap",
					LastChecked:     time.Now(),
				}

				// Extract additional info
				if pageInfo := c.extractPageInfo(ctx, catalogURL); pageInfo != nil {
					page.Title = pageInfo.Title
					page.ProductCount = pageInfo.ProductCount
					page.HasPagination = pageInfo.HasPagination
				}

				allCatalogPages = append(allCatalogPages, page)

				// Stop if we've reached the max pages limit
				if len(allCatalogPages) >= c.maxPages {
					break
				}
			}
		}

		if len(allCatalogPages) >= c.maxPages {
			break
		}
	}

	return allCatalogPages
}

// discoverFromLinks discovers catalog pages by analyzing homepage links
func (c *CatalogDiscovery) discoverFromLinks(ctx context.Context, baseURL string) []CatalogPage {
	c.logger.Info().Str("base_url", baseURL).Msg("Attempting catalog discovery via link analysis")

	// Fetch homepage
	result, err := c.httpScraper.Scrape(ctx, baseURL, Options{
		OutputFormat: "html",
	})

	if err != nil {
		c.logger.Warn().Err(err).Str("base_url", baseURL).Msg("Failed to fetch homepage for link analysis")
		return []CatalogPage{}
	}

	// Extract links using smart extractor's link extraction pattern
	linkRegex := regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*>(.+?)</a>`)
	matches := linkRegex.FindAllStringSubmatch(result.HTML, -1)

	var potentialCatalogURLs []string
	baseDomain := c.sitemapParser.GetBaseDomain(baseURL)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		href := match[1]
		linkText := strings.ToLower(match[2])

		// Check if link text or URL contains catalog indicators
		isCatalogLink := false
		for _, pattern := range c.catalogPatterns {
			if strings.Contains(href, pattern) ||
				strings.Contains(linkText, strings.Trim(pattern, "/")) {
				isCatalogLink = true
				break
			}
		}

		// Also check for common catalog-related keywords
		catalogKeywords := []string{
			"catalog", "shop", "store", "products", "goods",
			"каталог", "магазин", "товары", "продукты",
		}
		for _, keyword := range catalogKeywords {
			if strings.Contains(linkText, keyword) {
				isCatalogLink = true
				break
			}
		}

		if !isCatalogLink {
			continue
		}

		// Convert relative URLs to absolute
		if strings.HasPrefix(href, "/") {
			parsedURL, err := url.Parse(baseURL)
			if err != nil {
				continue
			}
			href = fmt.Sprintf("%s://%s%s", parsedURL.Scheme, parsedURL.Host, href)
		} else if !strings.HasPrefix(href, "http") {
			continue
		}

		// Ensure same domain
		if !strings.Contains(href, baseDomain) {
			continue
		}

		potentialCatalogURLs = append(potentialCatalogURLs, href)
	}

	// Validate potential catalog pages
	var catalogPages []CatalogPage
	for _, catalogURL := range potentialCatalogURLs {
		if len(catalogPages) >= c.maxPages {
			break
		}

		if c.validateCatalogPage(ctx, catalogURL) {
			page := CatalogPage{
				URL:             catalogURL,
				DiscoveryMethod: "links",
				LastChecked:     time.Now(),
			}

			if pageInfo := c.extractPageInfo(ctx, catalogURL); pageInfo != nil {
				page.Title = pageInfo.Title
				page.ProductCount = pageInfo.ProductCount
				page.HasPagination = pageInfo.HasPagination
			}

			catalogPages = append(catalogPages, page)
		}
	}

	return catalogPages
}

// discoverFromPatterns discovers catalog pages by trying common patterns
func (c *CatalogDiscovery) discoverFromPatterns(ctx context.Context, baseURL string) []CatalogPage {
	c.logger.Info().Str("base_url", baseURL).Msg("Attempting catalog discovery via pattern detection")

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return []CatalogPage{}
	}

	baseDomain := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	var catalogPages []CatalogPage

	// Try each catalog pattern
	for _, pattern := range c.catalogPatterns {
		if len(catalogPages) >= c.maxPages {
			break
		}

		catalogURL := baseDomain + pattern

		if c.validateCatalogPage(ctx, catalogURL) {
			page := CatalogPage{
				URL:             catalogURL,
				DiscoveryMethod: "patterns",
				LastChecked:     time.Now(),
			}

			if pageInfo := c.extractPageInfo(ctx, catalogURL); pageInfo != nil {
				page.Title = pageInfo.Title
				page.ProductCount = pageInfo.ProductCount
				page.HasPagination = pageInfo.HasPagination
			}

			catalogPages = append(catalogPages, page)
		}
	}

	return catalogPages
}

// validateCatalogPage checks if a page is actually a catalog page
func (c *CatalogDiscovery) validateCatalogPage(ctx context.Context, pageURL string) bool {
	result, err := c.httpScraper.Scrape(ctx, pageURL, Options{
		OutputFormat: "html",
		Timeout:      10 * time.Second,
	})

	if err != nil {
		c.logger.Debug().Err(err).Str("page_url", pageURL).Msg("Failed to validate catalog page")
		return false
	}

	// Check for catalog indicators
	html := strings.ToLower(result.HTML)

	// Product card patterns
	productPatterns := []string{
		`class="[^"]*product[^"]*"`,
		`class="[^"]*item[^"]*"`,
		`class="[^"]*goods[^"]*"`,
		`data-product`,
		`itemtype="https://schema.org/product"`,
	}

	hasProductPattern := false
	for _, pattern := range productPatterns {
		if matched, _ := regexp.MatchString(pattern, html); matched {
			hasProductPattern = true
			break
		}
	}

	// Price patterns
	pricePatterns := []string{
		`class="[^"]*price[^"]*"`,
		` itemprop="price"`,
		`\$\s*\d+`,
		`₽\s*\d+`,
		`€\s*\d+`,
	}

	hasPricePattern := false
	for _, pattern := range pricePatterns {
		if matched, _ := regexp.MatchString(pattern, html); matched {
			hasPricePattern = true
			break
		}
	}

	// Grid/list patterns
	gridPatterns := []string{
		`class="[^"]*grid[^"]*"`,
		`class="[^"]*products[^"]*"`,
		`class="[^"]*listing[^"]*"`,
	}

	hasGridPattern := false
	for _, pattern := range gridPatterns {
		if matched, _ := regexp.MatchString(pattern, html); matched {
			hasGridPattern = true
			break
		}
	}

	// Minimum requirements: at least product pattern + one more indicator
	isValid := hasProductPattern && (hasPricePattern || hasGridPattern)

	if isValid {
		c.logger.Debug().Str("page_url", pageURL).Msg("Page validated as catalog")
	}

	return isValid
}

// extractPageInfo extracts additional information from a catalog page
func (c *CatalogDiscovery) extractPageInfo(ctx context.Context, pageURL string) *CatalogPage {
	result, err := c.httpScraper.Scrape(ctx, pageURL, Options{
		OutputFormat: "html",
		Timeout:      10 * time.Second,
	})

	if err != nil {
		return nil
	}

	page := &CatalogPage{}

	// Extract title
	titleRegex := regexp.MustCompile(`<title[^>]*>(.+?)</title>`)
	if titleMatch := titleRegex.FindStringSubmatch(result.HTML); len(titleMatch) > 1 {
		page.Title = strings.TrimSpace(titleMatch[1])
	}

	// Count products
	productRegex := regexp.MustCompile(`class="[^"]*product[^"]*"`)
	products := productRegex.FindAllString(result.HTML, -1)
	page.ProductCount = len(products)

	// Check for pagination
	paginationPatterns := []string{
		`class="[^"]*pagination[^"]*"`,
		`class="[^"]*paging[^"]*"`,
		`href="[^"]*\?page=`,
		`href="[^"]*&page=`,
	}

	page.HasPagination = false
	for _, pattern := range paginationPatterns {
		if matched, _ := regexp.MatchString(pattern, result.HTML); matched {
			page.HasPagination = true
			break
		}
	}

	return page
}

// ExtractProducts extracts products from a catalog page HTML
func (c *CatalogDiscovery) ExtractProducts(html string) []Product {
	var products []Product

	c.logger.Info().Int("html_length", len(html)).Msg("ExtractProducts called")

	// STRATEGY 1: Use multiple approaches with increasing flexibility
	products = append(products, c.extractViaCSSSelectors(html)...)
	if len(products) > 0 {
		c.logger.Info().Int("products_count", len(products)).Msg("Successfully extracted products via CSS selectors")
		return products
	}

	// STRATEGY 2: Try flexible regex patterns for common structures
	products = append(products, c.extractViaFlexiblePatterns(html)...)
	if len(products) > 0 {
		c.logger.Info().Int("products_count", len(products)).Msg("Successfully extracted products via flexible patterns")
		return products
	}

	// STRATEGY 3: Try text-based extraction as last resort
	products = append(products, c.extractViaTextAnalysis(html)...)
	if len(products) > 0 {
		c.logger.Info().Int("products_count", len(products)).Msg("Successfully extracted products via text analysis")
		return products
	}

	c.logger.Warn().Msg("No products extracted with any method")
	return products
}

// extractViaCSSSelectors extracts products using CSS-like selectors
func (c *CatalogDiscovery) extractViaCSSSelectors(html string) []Product {
	var products []Product

	// Pattern 1: Elementor sub-menu items (most specific for kuycon-russia.ru)
	pattern1 := regexp.MustCompile(`<a[^>]*class="[^"]*elementor-sub-item[^"]*"[^>]*href="([^"]+)"[^>]*>(.+?)</a>`)
	matches1 := pattern1.FindAllStringSubmatch(html, -1)

	c.logger.Info().Int("elementor_items", len(matches1)).Msg("Trying Elementor sub-menu pattern")

	for _, match := range matches1 {
		if len(match) < 3 {
			continue
		}

		productURL := match[1]
		contentHTML := match[2]

		// Extract text content from HTML
		textContent := c.extractTextContent(contentHTML)
		if textContent == "" {
			continue
		}

		product := c.parseProductDescription(textContent)
		if product.Name != "" {
			product.ProductURL = productURL
			products = append(products, product)
		}
	}

	if len(products) > 0 {
		return products
	}

	// Pattern 2: General menu items
	pattern2 := regexp.MustCompile(`<li[^>]*class="menu-item[^"]*"[^>]*><a[^>]*href="([^"]+)"[^>]*>(.+?)</a></li>`)
	matches2 := pattern2.FindAllStringSubmatch(html, -1)

	c.logger.Info().Int("menu_items", len(matches2)).Msg("Trying general menu pattern")

	for _, match := range matches2 {
		if len(match) < 3 {
			continue
		}

		productURL := match[1]
		contentHTML := match[2]

		textContent := c.extractTextContent(contentHTML)
		if textContent == "" {
			continue
		}

		product := c.parseProductDescription(textContent)
		if product.Name != "" {
			product.ProductURL = productURL
			products = append(products, product)
		}
	}

	return products
}

// extractViaFlexiblePatterns extracts products using flexible regex patterns
func (c *CatalogDiscovery) extractViaFlexiblePatterns(html string) []Product {
	var products []Product

	// Pattern 1: Links with product-like content
	pattern1 := regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*class="[^"]*"[^>]*>([^<]+)</a>`)
	matches1 := pattern1.FindAllStringSubmatch(html, -1)

	c.logger.Info().Int("link_items", len(matches1)).Msg("Trying flexible link pattern")

	for _, match := range matches1 {
		if len(match) < 3 {
			continue
		}

		productURL := match[1]
		textContent := strings.TrimSpace(match[2])

		// Filter out non-product links
		if !c.looksLikeProduct(textContent, productURL) {
			continue
		}

		product := c.parseProductDescription(textContent)
		if product.Name != "" {
			product.ProductURL = productURL
			products = append(products, product)
		}
	}

	return products
}

// extractViaTextAnalysis extracts products by analyzing text patterns
func (c *CatalogDiscovery) extractViaTextAnalysis(html string) []Product {
	var products []Product

	// Split HTML into lines and analyze each line
	lines := strings.Split(html, "\n")

	c.logger.Info().Int("html_lines", len(lines)).Msg("Analyzing HTML text lines")

	for _, line := range lines {
		// Look for lines that contain product patterns
		if c.looksLikeProductLine(line) {
			// Try to extract URL from the line
			urlPattern := regexp.MustCompile(`href="([^"]+)"`)
			if urlMatch := urlPattern.FindStringSubmatch(line); len(urlMatch) > 1 {
				productURL := urlMatch[1]

				// Extract text content
				textContent := c.extractTextContent(line)
				if textContent != "" {
					c.logger.Debug().
						Str("product_url", productURL).
						Str("text_content", textContent).
						Msg("Found product via text analysis")

					product := c.parseProductDescription(textContent)
					if product.Name != "" {
						product.ProductURL = productURL
						products = append(products, product)

						c.logger.Info().
							Str("product_name", product.Name).
							Str("product_url", productURL).
							Interface("specs", product.Specs).
							Msg("Successfully parsed product via text analysis")
					}
				}
			}
		}
	}

	return products
}

// extractTextContent extracts clean text from HTML
func (c *CatalogDiscovery) extractTextContent(html string) string {
	// Remove HTML tags
	textRegex := regexp.MustCompile(`<[^>]+>`)
	text := textRegex.ReplaceAllString(html, " ")

	// Clean up whitespace
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)

	// Decode HTML entities
	text = c.decodeHTMLEntities(text)

	return text
}

// decodeHTMLEntities decodes common HTML entities
func (c *CatalogDiscovery) decodeHTMLEntities(text string) string {
	// Common HTML entities
	replacer := strings.NewReplacer(
		"&quot;", "\"",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&nbsp;", " ",
		"&#8212;", "—",
		"&#8211;", "–",
		"«", "«",
		"»", "»",
	)
	return replacer.Replace(text)
}

// looksLikeProduct checks if text/URL looks like a product
func (c *CatalogDiscovery) looksLikeProduct(text, url string) bool {
	// Must have meaningful length
	if len(text) < 5 || len(text) > 200 {
		return false
	}

	// Must contain alphanumeric characters
	hasAlpha := regexp.MustCompile(`[a-zA-Zа-яА-ЯёЁ]`).MatchString(text)
	if !hasAlpha {
		return false
	}

	textLower := strings.ToLower(text)
	textWords := strings.Fields(text)

	// БЛОК 1: Исключаем чистые категории без характеристик
	categoryOnlyPatterns := []string{
		"^профессиональные$", "^игровые$", "^gaming$", "^professional$",
		"^мониторы$", "^monitors$", "^для дома$", "^для офиса$",
		"^профессиональные мониторы$", "^игровые мониторы$",
	}

	for _, pattern := range categoryOnlyPatterns {
		if matched, _ := regexp.MatchString(pattern, textLower); matched {
			c.logger.Debug().Str("text", text).Str("rejected_by", "category_only").Msg("Rejected as category-only")
			return false
		}
	}

	// БЛОК 2: Если текст содержит только категорию (1-2 слова) - это не продукт
	if len(textWords) <= 2 {
		genericWords := []string{"профессиональные", "игровые", "мониторы", "monitors",
			"professional", "gaming", "для", "офиса", "дома", "home", "office"}
		for _, word := range textWords {
			lowerWord := strings.ToLower(word)
			for _, genericWord := range genericWords {
				if strings.Contains(lowerWord, genericWord) {
					c.logger.Debug().Str("text", text).Str("rejected_by", "generic_word").Msg("Rejected as generic")
					return false
				}
			}
		}
	}

	// БЛОК 3: Проверяем на наличие модельного кода или характеристик
	hasModelCode := false
	hasSpecs := false

	// Model patterns: P40K, Q34, G27, etc.
	modelPatterns := []string{
		"[PQGM]\\d+[A-Z]*", "\\d+[Kk]\\s*—\\s*\\d+", "\\d+[Kk]\\s*–\\s*\\d+",
	}

	for _, pattern := range modelPatterns {
		if matched, _ := regexp.MatchString(pattern, text); matched {
			hasModelCode = true
			break
		}
	}

	// Spec patterns: 120 Гц, 5K, etc.
	specPatterns := []string{
		"\\d+\\s*[Kkк]\\s*[ГцHhz]*", "\\d+\\s*[ГцHhz]",
		"5K", "4K", "2K", "8K",
	}

	for _, pattern := range specPatterns {
		if matched, _ := regexp.MatchString(pattern, text); matched {
			hasSpecs = true
			break
		}
	}

	// БЛОК 4: Если есть характеристики - точно продукт
	if hasSpecs {
		c.logger.Debug().Str("text", text).Str("accepted_by", "has_specs").Msg("Accepted as product (has specs)")
		return true
	}

	// БЛОК 5: Если есть модельный код - возможно продукт, но нужна дополнительная проверка
	if hasModelCode {
		// Проверяем URL - если это /catalog или /shop, то скорее категория
		if !strings.Contains(url, "/catalog") && !strings.Contains(url, "/shop") {
			c.logger.Debug().Str("text", text).Str("accepted_by", "model_code_in_url").Msg("Accepted as product (model code + URL)")
			return true
		}
	}

	c.logger.Debug().Str("text", text).Str("rejected_by", "no_model_or_specs").Msg("Rejected (no model code or specs)")
	return false
}

// looksLikeProductLine checks if a line looks like it contains product info
func (c *CatalogDiscovery) looksLikeProductLine(line string) bool {
	// Must contain a link
	if !strings.Contains(line, "href=") {
		return false
	}

	// УНИВЕРСАЛЬНЫЕ ПАТТЕРНЫ для характеристик продуктов
	// Вместо конкретных моделей ищем паттерны характеристик

	// Паттерны разрешения: 5K, 4K, 2K, 8K, etc.
	if matched, _ := regexp.MatchString(`\d+[KkKk](?:\s|[-—–]|$)`, line); matched {
		return true
	}

	// Паттерны частоты: 120 Гц, 165Hz, 240Hz, etc.
	if matched, _ := regexp.MatchString(`\d+\s*(?:Гц|Hz|hz)\b`, line); matched {
		return true
	}

	// Паттерны модельных кодов: буква+цифры (P40, Q34, G27, etc.)
	if matched, _ := regexp.MatchString(`[A-Z]\d{2,}[A-Z]*\b`, line); matched {
		return true
	}

	// Паттерны размеров: 27", 32", 34" with inch/дюйм
	if matched, _ := regexp.MatchString(`\d+\s*(?:["']|inch|дюйм)`, line); matched {
		return true
	}

	// Паттерны соотношений сторон: 16:9, 21:9, 16:10
	if matched, _ := regexp.MatchString(`\d+:\d+`, line); matched {
		return true
	}

	// Паттерны технологий: IPS, VA, TN, OLED
	techPatterns := []string{" IPS", " VA", " TN", " OLED", " LED"}
	for _, tech := range techPatterns {
		if strings.Contains(line, tech) {
			return true
		}
	}

	return false
}

// parseMenuProduct parses products from text descriptions (unified approach)
func (c *CatalogDiscovery) parseMenuProduct(html string) Product {
	// Extract text and parse as description
	textContent := c.extractTextContent(html)
	return c.parseProductDescription(textContent)
}

// parseProductDescription parses product from text description
func (c *CatalogDiscovery) parseProductDescription(text string) Product {
	product := Product{
		Specs: make(map[string]string),
	}

	if text == "" {
		return product
	}

	// Clean up the text
	text = strings.TrimSpace(text)
	text = c.decodeHTMLEntities(text)

	c.logger.Debug().Str("input_text", text).Msg("Parsing product description")

	// Parse the description using multiple patterns
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '—' || r == '-' || r == '–' || r == '|'
	})

	c.logger.Debug().Interface("parts", parts).Msg("Split text into parts")

	if len(parts) == 0 {
		// Если нет разделителей, пробуем другой подход
		return c.parseProductWithoutSeparators(text)
	}

	// УНИВЕРСАЛЬНЫЙ ПАТТЕРН: "Код модели — характеристики — категория"
	// Пример: "P40K — 5K — 120 Гц — Профессиональные"

	// 2. Анализируем каждую часть и классифицируем
	var modelCode string
	var resolution string
	var frequency string
	var category string

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		c.logger.Debug().Int("part_index", i).Str("part_content", part).Msg("Analyzing part")

		// Проверяем на модельный код (P40K, Q34, G27, etc.)
		if modelCode == "" {
			if matched, _ := regexp.MatchString(`^[A-Z]\d+[A-Z]*$`, part); matched {
				modelCode = part
				c.logger.Debug().Str("extracted_model", modelCode).Msg("Found model code")
				continue
			}
		}

		// Проверяем на разрешение (5K, 4K, 2K, etc.)
		if resolution == "" {
			if matched, _ := regexp.MatchString(`^\d+[Kk]$`, part); matched {
				resolution = strings.ToUpper(part)
				c.logger.Debug().Str("extracted_resolution", resolution).Msg("Found resolution")
				continue
			}
		}

		// Проверяем на частоту (120 Гц, 165 Hz, etc.)
		if frequency == "" {
			freqRegex := regexp.MustCompile(`(\d+)\s*(?:Гц|Hz|hz)?`)
			if freqMatch := freqRegex.FindStringSubmatch(part); len(freqMatch) > 1 {
				// Убеждаемся что это не просто число (может быть размер)
				if strings.Contains(strings.ToLower(part), "гц") ||
					strings.Contains(strings.ToLower(part), "hz") ||
					len(strings.Fields(part)) == 1 {
					frequency = freqMatch[1] + "Hz"
					c.logger.Debug().Str("extracted_frequency", frequency).Msg("Found frequency")
					continue
				}
			}
		}

		// Если часть похожа на категорию (последняя часть, длинная, без цифр)
		if i == len(parts)-1 && len(strings.Fields(part)) <= 3 {
			if !regexp.MustCompile(`\d`).MatchString(part) {
				category = part
				c.logger.Debug().Str("extracted_category", category).Msg("Found category")
			}
		}
	}

	// 3. Формируем результат
	if modelCode != "" {
		product.Name = modelCode
	} else {
		// Если модельный код не найден, но есть характеристики,
		// пробуем извлечь из первой части
		if len(parts) > 0 {
			firstPart := strings.TrimSpace(parts[0])
			words := strings.Fields(firstPart)
			if len(words) > 0 {
				// Первое слово может быть моделью
				candidate := words[0]
				// Проверяем что это не общее слово
				if !regexp.MustCompile(`^(профессиональные|игровые|мониторы|monitors|professional|gaming)$`).MatchString(strings.ToLower(candidate)) {
					product.Name = candidate
				}
			}
		}
	}

	// Добавляем характеристики
	if resolution != "" {
		product.Specs["resolution"] = resolution
	}
	if frequency != "" {
		product.Specs["frequency"] = frequency
	}

	// Добавляем описание если нашли productName
	if product.Name != "" {
		product.Description = text
	}

	c.logger.Debug().
		Str("final_name", product.Name).
		Interface("final_specs", product.Specs).
		Msg("Parsed product")

	return product
}

// parseProductWithoutSeparators parses product when text has no separators
func (c *CatalogDiscovery) parseProductWithoutSeparators(text string) Product {
	product := Product{
		Specs: make(map[string]string),
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return product
	}

	// Ищем паттерны в тексте без разделителей
	for _, word := range words {
		word = strings.TrimSpace(word)

		// Модельный код
		if matched, _ := regexp.MatchString(`^[A-Z]\d+[A-Z]*$`, word); matched {
			product.Name = word
			continue
		}

		// Разрешение
		if matched, _ := regexp.MatchString(`^\d+[Kk]`, word); matched {
			product.Specs["resolution"] = strings.ToUpper(word)
			continue
		}

		// Частота
		if regexp.MustCompile(`\d+\s*(?:Гц|Hz)`).MatchString(word) {
			freqRegex := regexp.MustCompile(`(\d+)\s*(?:Гц|Hz)`)
			if freqMatch := freqRegex.FindStringSubmatch(word); len(freqMatch) > 1 {
				product.Specs["frequency"] = freqMatch[1] + "Hz"
				continue
			}
		}
	}

	if product.Name != "" {
		product.Description = text
	}

	return product
}

// parseSchemaProduct parses Schema.org Product markup
func (c *CatalogDiscovery) parseSchemaProduct(html string) Product {
	product := Product{
		Specs: make(map[string]string),
	}

	// Extract product name
	namePatterns := []string{
		`<h[23][^>]*class="[^"]*name[^"]*"[^>]*>(.+?)</h[23]>`,
		`<h[23][^>]*class="[^"]*title[^"]*"[^>]*>(.+?)</h[23]>`,
		`itemprop="name"[^>]*>(.+?)</`,
	}

	for _, namePattern := range namePatterns {
		if nameRegex := regexp.MustCompile(namePattern); nameRegex.MatchString(html) {
			if nameMatch := nameRegex.FindStringSubmatch(html); len(nameMatch) > 1 {
				product.Name = strings.TrimSpace(nameMatch[1])
				break
			}
		}
	}

	// Extract price
	pricePatterns := []string{
		`class="[^"]*price[^"]*"[^>]*>(.+?)</`,
		`itemprop="price"[^>]*content="([^"]+)"`,
	}

	for _, pricePattern := range pricePatterns {
		if priceRegex := regexp.MustCompile(pricePattern); priceRegex.MatchString(html) {
			if priceMatch := priceRegex.FindStringSubmatch(html); len(priceMatch) > 1 {
				product.Price = strings.TrimSpace(priceMatch[1])
				break
			}
		}
	}

	// Extract specs
	specPatterns := map[string]string{
		"frequency":  `(?:частота|frequency|hz|гц)[^:]*:\s*(\d+)`,
		"resolution": `(?:разрешение|resolution)[^:]*:\s*([\d×x]+)`,
		"brightness": `(?:яркость|brightness)[^:]*:\s*(\d+)`,
		"size":       `(?:размер|size|дюйм|inch)[^:]*:\s*(\d+)`,
	}

	for specName, specPattern := range specPatterns {
		if specRegex := regexp.MustCompile(specPattern); specRegex.MatchString(html) {
			if specMatch := specRegex.FindStringSubmatch(html); len(specMatch) > 1 {
				product.Specs[specName] = strings.TrimSpace(specMatch[1])
			}
		}
	}

	return product
}

// parseGenericProduct parses generic product cards
func (c *CatalogDiscovery) parseGenericProduct(html string) Product {
	return c.parseSchemaProduct(html) // Reuse schema parsing logic
}
