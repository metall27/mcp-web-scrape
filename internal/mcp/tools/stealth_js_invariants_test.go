package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// TestStealthJSInvariantsOnChrome verifies the #95 Stage 2 acceptance
// criteria on a live headless Chrome — the exact probes an anti-bot would
// run — using the same scripts the scraper injects:
//
//   - typeof Date.prototype.getTimezoneOffset === 'function' and callable
//   - window.screen === window.screen → true
//   - Number.isInteger(Date.now()) → true
//   - Date.toString() carries a human-readable zone name, not an IANA id
//   - hardwareConcurrency is a plausible stable integer
//
// Uses a data: URL page — no network needed, only a Chrome binary (the
// docker-test image ships one). Skipped in -short mode.
func TestStealthJSInvariantsOnChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Chrome binary")
	}

	profile := browser.NewProfile(
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	stealth := browser.NewStealthActions(browser.StealthConfig{})

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

	var probe struct {
		TZTypeof       string `json:"tzTypeof"`
		TZValue        int    `json:"tzValue"`
		TZThrew        bool   `json:"tzThrew"`
		ScreenIdentity bool   `json:"screenIdentity"`
		DateNowInt     bool   `json:"dateNowInt"`
		DateToString   string `json:"dateToString"`
		LiveZoneName   string `json:"liveZoneName"`
		Cores          int    `json:"cores"`
		ScreenWidth    int    `json:"screenWidth"`
	}

	err := chromedp.Run(runCtx,
		// The same overrides the scraper applies (buildChromeTasks Phase 1/3).
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := emulation.SetUserAgentOverride(profile.UserAgent).
				WithAcceptLanguage(profile.AcceptLanguage()).
				WithPlatform(profile.Platform).
				WithUserAgentMetadata(profile.UserAgentMetadata()).
				Do(ctx); err != nil {
				return err
			}
			if err := emulation.SetTimezoneOverride(profile.Timezone).Do(ctx); err != nil {
				t.Logf("CDP timezone override failed: %v (JS fallback covers it)", err)
			}
			return nil
		}),
		// Inject the real stealth scripts (persisted on new documents).
		stealth.InjectAntiDetectionScripts(profile),
		chromedp.Navigate(`data:text/html,<html><body><h1>probe</h1></body></html>`),
		chromedp.Evaluate(fmt.Sprintf(`(() => {
			let tzValue = null, tzThrew = false;
			try { tzValue = new Date().getTimezoneOffset(); } catch (e) { tzThrew = true; }
			// Live seasonal name from Intl — what a real Chrome in the
			// profile's zone would print RIGHT NOW (review finding 3:
			// asserting against the hardcoded profile name cannot catch
			// winter "Daylight" contradictions).
			const liveName = new Intl.DateTimeFormat('en-US', {
				timeZone: %q, timeZoneName: 'long'
			}).formatToParts(new Date()).find(p => p.type === 'timeZoneName').value;
			return {
				tzTypeof: typeof Date.prototype.getTimezoneOffset,
				tzValue: tzValue,
				tzThrew: tzThrew,
				screenIdentity: window.screen === window.screen,
				dateNowInt: Number.isInteger(Date.now()),
				dateToString: new Date().toString(),
				liveZoneName: liveName,
				cores: navigator.hardwareConcurrency,
				screenWidth: screen.width
			};
		})()`, profile.Timezone), &probe),
	)
	if err != nil {
		t.Fatalf("chromedp run failed: %v", err)
	}

	if probe.TZTypeof != "function" {
		t.Errorf("typeof getTimezoneOffset = %q, want 'function'", probe.TZTypeof)
	}
	if probe.TZThrew {
		t.Error("new Date().getTimezoneOffset() threw — the old getter-override bug")
	}
	if probe.TZValue == 0 && profile.Timezone != "Europe/London" {
		t.Errorf("getTimezoneOffset() = %d for zone %q — looks like the override failed", probe.TZValue, profile.Timezone)
	}
	if !probe.ScreenIdentity {
		t.Error("window.screen === window.screen is false — screen object is replaced per access")
	}
	if !probe.DateNowInt {
		t.Error("Number.isInteger(Date.now()) is false — fractional jitter leaked into Date.now")
	}
	if strings.Contains(probe.DateToString, "/") && strings.Contains(probe.DateToString, "(") {
		// An IANA id like "(America/New_York)" contains a slash inside parens.
		open := strings.Index(probe.DateToString, "(")
		closeIdx := strings.Index(probe.DateToString, ")")
		if open >= 0 && closeIdx > open && strings.Contains(probe.DateToString[open:closeIdx], "/") {
			t.Errorf("Date.toString() leaks IANA id: %s", probe.DateToString)
		}
	}
	if !strings.Contains(probe.DateToString, "("+probe.LiveZoneName+")") {
		t.Errorf("Date.toString() = %q, want seasonal zone name (%s) from live Intl",
			probe.DateToString, probe.LiveZoneName)
	}
	if probe.Cores <= 0 || probe.Cores > 64 {
		t.Errorf("hardwareConcurrency = %d, implausible", probe.Cores)
	}
	if probe.ScreenWidth != profile.ScreenWidth {
		t.Errorf("screen.width = %d, want profile value %d", probe.ScreenWidth, profile.ScreenWidth)
	}
}

// TestStealthStableAcrossReloads verifies the fingerprint does not change
// between page reloads within one browser context (#95 acceptance: cores
// must not morph 8→4→16 on reload).
func TestStealthStableAcrossReloads(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Chrome binary")
	}

	profile := browser.NewProfile(
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	stealth := browser.NewStealthActions(browser.StealthConfig{})

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

	var first, second map[string]interface{}
	err := chromedp.Run(runCtx,
		stealth.InjectAntiDetectionScripts(profile),
		chromedp.Navigate(`data:text/html,<html><body>v1</body></html>`),
		chromedp.Evaluate(`(() => ({
			cores: navigator.hardwareConcurrency,
			memory: navigator.deviceMemory || 0,
			w: screen.width,
			h: screen.height,
			platform: navigator.platform
		}))()`, &first),
		// Reload: AddScriptToEvaluateOnNewDocument re-runs on the new document.
		chromedp.ActionFunc(func(ctx context.Context) error {
			return page.Reload().Do(ctx)
		}),
		chromedp.Evaluate(`(() => ({
			cores: navigator.hardwareConcurrency,
			memory: navigator.deviceMemory || 0,
			w: screen.width,
			h: screen.height,
			platform: navigator.platform
		}))()`, &second),
	)
	if err != nil {
		t.Fatalf("chromedp run failed: %v", err)
	}

	for _, key := range []string{"cores", "memory", "w", "h", "platform"} {
		if first[key] != second[key] {
			t.Errorf("fingerprint key %q changed across reload: %v → %v", key, first[key], second[key])
		}
	}
	if first["platform"] != profile.Platform {
		t.Errorf("navigator.platform = %v, want %q", first["platform"], profile.Platform)
	}
}
