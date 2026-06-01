package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// BlockType тип блокировки
type BlockType string

const (
	BlockTypeCloudflare   BlockType = "cloudflare"
	BlockTypeCaptcha      BlockType = "captcha"
	BlockTypeRateLimit    BlockType = "rate_limit"
	BlockTypeAccessDenied BlockType = "access_denied"
	BlockTypeUnknown      BlockType = "unknown"
)

// BlockResult результат детекта
type BlockResult struct {
	IsBlocked  bool
	BlockType  BlockType
	Details    string
	Confidence float64 // 0.0 to 1.0
}

// DetectBlocking проверяет страницу на наличие блокировок
func DetectBlocking(ctx context.Context) (BlockResult, error) {
	var pageInfo struct {
		Title       string
		BodyHTML    string
		URL         string
		HeadScripts []string
	}

	// Get page information
	err := chromedp.ActionFunc(func(ctx context.Context) error {
		// Get title
		if err := chromedp.Title(&pageInfo.Title).Do(ctx); err != nil {
			return err
		}

		// Get URL
		if err := chromedp.Location(&pageInfo.URL).Do(ctx); err != nil {
			return err
		}

		// Get body HTML
		if err := chromedp.InnerHTML("body", &pageInfo.BodyHTML, chromedp.ByQuery).Do(ctx); err != nil {
			return err
		}

		// Get head scripts for fingerprinting
		var scripts []string
		err := chromedp.Evaluate(`
			(() => {
				return Array.from(document.querySelectorAll('head script'))
					.map(s => s.src || s.textContent).filter(Boolean);
			})()
		`, &scripts).Do(ctx)

		if err == nil {
			pageInfo.HeadScripts = scripts
		}

		return nil
	}).Do(ctx)

	if err != nil {
		return BlockResult{}, err
	}

	// Analyze for blocks
	return analyzeBlocking(pageInfo)
}

// analyzeBlocking анализирует страницу на признаки блокировок
func analyzeBlocking(pageInfo struct {
	Title       string
	BodyHTML    string
	URL         string
	HeadScripts []string
}) (BlockResult, error) {
	result := BlockResult{IsBlocked: false, Confidence: 0.0}

	// Check Cloudflare indicators
	if isCloudflareBlocked(pageInfo) {
		result.IsBlocked = true
		result.BlockType = BlockTypeCloudflare
		result.Details = "Cloudflare protection detected"
		result.Confidence = 0.95
		return result, nil
	}

	// Check CAPTCHA indicators
	if isCaptchaBlocked(pageInfo) {
		result.IsBlocked = true
		result.BlockType = BlockTypeCaptcha
		result.Details = "CAPTCHA challenge detected"
		result.Confidence = 0.9
		return result, nil
	}

	// Check rate limit indicators
	if isRateLimited(pageInfo) {
		result.IsBlocked = true
		result.BlockType = BlockTypeRateLimit
		result.Details = "Rate limit detected"
		result.Confidence = 0.85
		return result, nil
	}

	// Check access denied
	if isAccessDenied(pageInfo) {
		result.IsBlocked = true
		result.BlockType = BlockTypeAccessDenied
		result.Details = "Access denied (403/401)"
		result.Confidence = 0.8
		return result, nil
	}

	return result, nil
}

// isCloudflareBlocked проверяет индикаторы Cloudflare
func isCloudflareBlocked(pageInfo struct{ Title, BodyHTML, URL string; HeadScripts []string }) bool {
	indicators := []string{
		"cloudflare",
		"cf-challenge",
		"cf_browser_verification",
		"__cf_bm",
		"challenge-platform",
		"cf-",
	}

	combinedText := strings.ToLower(pageInfo.Title + " " + pageInfo.BodyHTML + " " + pageInfo.URL)
	combinedScripts := strings.ToLower(strings.Join(pageInfo.HeadScripts, " "))

	for _, indicator := range indicators {
		if strings.Contains(combinedText, indicator) || strings.Contains(combinedScripts, indicator) {
			// Additional checks for Cloudflare
			if strings.Contains(combinedText, "checking your browser") ||
				strings.Contains(combinedText, "attention required") {
				return true
			}
		}
	}

	// Check for Cloudflare-specific HTML structure
	cloudflareSelectors := []string{
		"cf-error-details",
		"cf-challenge-platform",
		"challenge-form",
		"captcha-bypass",
	}

	for _, selector := range cloudflareSelectors {
		if strings.Contains(combinedText, selector) {
			return true
		}
	}

	return false
}

// isCaptchaBlocked проверяет индикаторы CAPTCHA
func isCaptchaBlocked(pageInfo struct{ Title, BodyHTML, URL string; HeadScripts []string }) bool {
	indicators := []string{
		"captcha",
		"recaptcha",
		"hcaptcha",
		"turnstile",
		"g-recaptcha",
		"verify you are human",
		"prove you're not a robot",
	}

	combinedText := strings.ToLower(pageInfo.Title + " " + pageInfo.BodyHTML)

	for _, indicator := range indicators {
		if strings.Contains(combinedText, indicator) {
			return true
		}
	}

	// Check for common CAPTCHA domains
	captchaDomains := []string{
		"www.google.com/recaptcha",
		"hcaptcha.com",
		"challenges.cloudflare.com",
		"turnstile",
	}

	combinedScripts := strings.Join(pageInfo.HeadScripts, " ")
	for _, domain := range captchaDomains {
		if strings.Contains(combinedScripts, domain) {
			return true
		}
	}

	return false
}

// isRateLimited проверяет индикаторы rate limiting
func isRateLimited(pageInfo struct{ Title, BodyHTML, URL string; HeadScripts []string }) bool {
	indicators := []string{
		"rate limit",
		"too many requests",
		"429",
		"request limit",
		"try again later",
	}

	combinedText := strings.ToLower(pageInfo.Title + " " + pageInfo.BodyHTML)

	for _, indicator := range indicators {
		if strings.Contains(combinedText, indicator) {
			return true
		}
	}

	return false
}

// isAccessDenied проверяет индикаторы access denied
func isAccessDenied(pageInfo struct{ Title, BodyHTML, URL string; HeadScripts []string }) bool {
	indicators := []string{
		"access denied",
		"forbidden",
		"403",
		"401",
		"unauthorized",
		"authorization required",
	}

	combinedText := strings.ToLower(pageInfo.Title + " " + pageInfo.BodyHTML)

	for _, indicator := range indicators {
		if strings.Contains(combinedText, indicator) {
			return true
		}
	}

	return false
}

// String возвращает строковое представление BlockType
func (bt BlockType) String() string {
	return string(bt)
}

// String возвращает детальное описание BlockResult
func (br BlockResult) String() string {
	if !br.IsBlocked {
		return "No blocking detected"
	}
	return fmt.Sprintf("Block detected: type=%s, confidence=%.2f, details=%s",
		br.BlockType, br.Confidence, br.Details)
}
