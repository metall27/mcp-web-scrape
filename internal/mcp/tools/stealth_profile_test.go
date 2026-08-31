package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// TestStealthProfileConsistency verifies the #95 P0 acceptance criteria
// against a real headless Chrome: the advertised identity must be coherent
// at every layer — HTTP header, Sec-CH-UA client hints, and the JS
// navigator object — with no "Headless" anywhere.
//
// Integration test: requires a local Chromium/Chrome binary (the docker-test
// image ships one) and network access to httpbin.org / example.com.
// Skipped in -short mode.
func TestStealthProfileConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Chrome + network")
	}

	profile := browser.NewProfile(
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", "new"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, 60*time.Second)
	defer runCancel()

	// 1. HTTP-header layer: httpbin echoes the request headers back.
	var bodyText string
	err := chromedp.Run(runCtx,
		// The same override the scraper applies (buildChromeTasks Phase 1).
		chromedp.ActionFunc(func(ctx context.Context) error {
			return emulation.SetUserAgentOverride(profile.UserAgent).
				WithAcceptLanguage(profile.AcceptLanguage()).
				WithPlatform(profile.Platform).
				WithUserAgentMetadata(profile.UserAgentMetadata()).
				Do(ctx)
		}),
		chromedp.Navigate("https://httpbin.org/headers"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Evaluate(`document.body.innerText`, &bodyText),
	)
	if err != nil {
		t.Skipf("httpbin.org unreachable (%v) — network-dependent test", err)
	}

	var parsed struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(bodyText), &parsed); err != nil {
		t.Fatalf("failed to parse httpbin response %q: %v", bodyText, err)
	}

	httpUA := parsed.Headers["User-Agent"]
	if strings.Contains(httpUA, "Headless") {
		t.Errorf("HTTP User-Agent contains Headless: %s", httpUA)
	}
	if httpUA != profile.UserAgent {
		t.Errorf("HTTP UA %q != profile UA %q", httpUA, profile.UserAgent)
	}

	// 2. JS layer: navigator must agree with the HTTP header.
	var probe struct {
		UA         string `json:"ua"`
		AppVersion string `json:"appVersion"`
		Platform   string `json:"platform"`
		Brands     string `json:"brands"`
		CHPlatform string `json:"chPlatform"`
	}
	err = chromedp.Run(runCtx,
		chromedp.Navigate("https://example.com"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Evaluate(`(() => ({
			ua: navigator.userAgent,
			appVersion: navigator.appVersion,
			platform: navigator.platform,
			brands: ((navigator.userAgentData && navigator.userAgentData.brands) || []).map(b => b.brand).join(','),
			chPlatform: navigator.userAgentData && navigator.userAgentData.platform
		}))()`, &probe),
	)
	if err != nil {
		t.Skipf("example.com unreachable (%v) — network-dependent test", err)
	}

	if strings.Contains(probe.UA, "Headless") || probe.UA != profile.UserAgent {
		t.Errorf("navigator.userAgent = %q, want %q", probe.UA, profile.UserAgent)
	}
	// #95 item 16: navigator.appVersion comes FREE with the Emulation
	// override — it must be the UA minus the "Mozilla/" prefix and must
	// not contain "Headless".
	if strings.Contains(probe.AppVersion, "Headless") {
		t.Errorf("navigator.appVersion contains Headless: %s", probe.AppVersion)
	}
	if want := strings.TrimPrefix(profile.UserAgent, "Mozilla/"); probe.AppVersion != want {
		t.Errorf("navigator.appVersion = %q, want %q", probe.AppVersion, want)
	}
	if probe.Platform != profile.Platform {
		t.Errorf("navigator.platform = %q, want %q", probe.Platform, profile.Platform)
	}
	if strings.Contains(probe.Brands, "Headless") {
		t.Errorf("userAgentData brands contain Headless: %s", probe.Brands)
	}
	if probe.CHPlatform != profile.CHPlatform {
		t.Errorf("userAgentData.platform = %q, want %q", probe.CHPlatform, profile.CHPlatform)
	}
}
