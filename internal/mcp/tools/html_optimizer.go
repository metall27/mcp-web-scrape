package tools

import (
	"regexp"
	"strings"
)

// OptimizeHTML removes noise from HTML to reduce token count for inference
func OptimizeHTML(html []byte) []byte {
	htmlStr := string(html)

	// Remove entire head section (stylesheets, scripts, meta tags)
	headRegex := regexp.MustCompile(`(?is)<head>.*?</head>`)
	htmlStr = headRegex.ReplaceAllString(htmlStr, "")

	// Remove script tags and their content
	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStr = scriptRegex.ReplaceAllString(htmlStr, "")

	// Remove style tags and their content
	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlStr = styleRegex.ReplaceAllString(htmlStr, "")

	// Remove HTML comments
	commentRegex := regexp.MustCompile(`<!--.*?-->`)
	htmlStr = commentRegex.ReplaceAllString(htmlStr, "")

	// Remove link tags (stylesheets, DNS prefetch, etc)
	linkRegex := regexp.MustCompile(`<link[^>]+>`)
	htmlStr = linkRegex.ReplaceAllString(htmlStr, "")

	// Remove all meta tags (not needed for content extraction)
	metaRegex := regexp.MustCompile(`<meta[^>]+>`)
	htmlStr = metaRegex.ReplaceAllString(htmlStr, "")

	// Remove UI elements that don't contain content
	uiElements := []struct {
		pattern string
		reason  string
	}{
		// Navigation
		{`(?is)<nav[^>]*>.*?</nav>`, "navigation"},
		{`(?is)<header[^>]*>.*?</header>`, "header"},
		{`(?is)<footer[^>]*>.*?</footer>`, "footer"},

		// Buttons and forms
		{`(?is)<button[^>]*>.*?</button>`, "buttons"},
		{`(?is)<input[^>]+>`, "inputs"},
		{`(?is)<textarea[^>]*>.*?</textarea>`, "textareas"},
		{`(?is)<select[^>]*>.*?</select>`, "selects"},

		// Loaders and placeholders
		{`<div[^>]*class="[^"]*\b(?:loading|skeleton|spinner|placeholder)\b[^"]*"[^>]*>.*?</div>`, "loaders"},

		// Iframes and embeds
		{`(?is)<iframe[^>]*>.*?</iframe>`, "iframes"},
		{`(?is)<embed[^>]+>`, "embeds"},
		{`(?is)<object[^>]*>.*?</object>`, "objects"},

		// Templates and lazy-loaded fragments
		{`<template[^>]*>.*?</template>`, "templates"},
		{`<include-fragment[^>]*>.*?</include-fragment>`, "fragments"},

		// SVG icons
		{`(?is)<svg[^>]*>.*?</svg>`, "svg icons"},

		// Noscript tags
		{`(?is)<noscript[^>]*>.*?</noscript>`, "noscript"},

		// Details/summary (accordions, dropdowns)
		{`(?is)<details[^>]*>.*?</details>`, "dropdowns"},
	}

	for _, p := range uiElements {
		re := regexp.MustCompile(p.pattern)
		htmlStr = re.ReplaceAllString(htmlStr, "")
	}

	// Remove ALL span tags (mostly inline wrappers)
	spanRegex := regexp.MustCompile(`(?is)</?span[^>]*>`)
	htmlStr = spanRegex.ReplaceAllString(htmlStr, "")

	// Remove empty divs
	emptyDivRegex := regexp.MustCompile(`<div[^>]*>\s*</div>`)
	htmlStr = emptyDivRegex.ReplaceAllString(htmlStr, "")

	// Simplify attributes - remove all data-* attributes
	dataAttrRegex := regexp.MustCompile(`\s+data-[a-z0-9-]+="[^"]*"`)
	htmlStr = dataAttrRegex.ReplaceAllString(htmlStr, "")

	// Remove aria attributes (keep content, remove accessibility metadata)
	ariaAttrRegex := regexp.MustCompile(`\s+aria-[a-z0-9-]+="[^"]*"`)
	htmlStr = ariaAttrRegex.ReplaceAllString(htmlStr, "")

	// Remove id attributes
	idAttrRegex := regexp.MustCompile(`\s+id="[^"]*"`)
	htmlStr = idAttrRegex.ReplaceAllString(htmlStr, "")

	// Simplify class attributes (remove utility classes, keep semantic)
	simplifyClassRegex := regexp.MustCompile(`\s+class="[^"]*\b(?:btn|nav|footer|header|sidebar|wrapper|container|loading|skeleton|spinner)\b[^"]*"`)
	htmlStr = simplifyClassRegex.ReplaceAllString(htmlStr, "")

	// Collapse whitespace
	htmlStr = regexp.MustCompile(`\s+`).ReplaceAllString(htmlStr, " ")
	htmlStr = regexp.MustCompile(`>\s+<`).ReplaceAllString(htmlStr, "><")
	htmlStr = strings.TrimSpace(htmlStr)

	return []byte(htmlStr)
}

// OptimizeGitHubHTML applies GitHub-specific optimizations on top of general
func OptimizeGitHubHTML(html []byte) []byte {
	htmlStr := string(html)

	// Apply general optimization first
	htmlStr = string(OptimizeHTML(html))

	// GitHub-specific removals
	githubPatterns := []struct {
		pattern string
		reason  string
	}{
		// GitHub-specific UI components
		{`<div[^>]*class="[^"]*\bPageLayout-PaneWrapper\b[^"]*"[^>]*>.*?</div>`, "sidebar panels"},
		{`<div[^>]*class="[^"]*\breact-directory\b[^"]*"[^>]*>.*?</div>`, "react directory"},
		{`(?is)<rails-partial[^>]*>.*?</rails-partial>`, "rails partials"},
		{`<li[^>]*class="[^"]*\bNavLink\b[^"]*"[^>]*>.*?</li>`, "nav links"},

		// GitHub skeleton loaders (specific class names)
		{`<div[^>]*class="[^"]*\bSkeleton\b[^"]*"[^>]*>.*?</div>`, "skeleton loaders"},

		// Empty utility containers
		{`<div[^>]*class="[^"]*\b(?:overflow-hidden|d-flex|flex-.*?|Box\b)[^"]*"[^>]*>\s*</div>`, "empty utility divs"},
	}

	for _, p := range githubPatterns {
		re := regexp.MustCompile(p.pattern)
		htmlStr = re.ReplaceAllString(htmlStr, "")
	}

	// Final cleanup
	htmlStr = regexp.MustCompile(`\s+`).ReplaceAllString(htmlStr, " ")
	htmlStr = regexp.MustCompile(`>\s+<`).ReplaceAllString(htmlStr, "><")
	htmlStr = strings.TrimSpace(htmlStr)

	return []byte(htmlStr)
}
