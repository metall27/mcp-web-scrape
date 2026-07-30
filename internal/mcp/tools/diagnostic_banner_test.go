package tools

import (
	"strings"
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// TestBuildDiagnosticBannerCleanScrape verifies that a result with no auth /
// blocking / fallback signals produces NO banner — zero overhead on success.
func TestBuildDiagnosticBannerCleanScrape(t *testing.T) {
	result := &Result{
		URL:      "https://example.com/page",
		FinalURL: "https://example.com/page",
		HTML:     "<h1>Real content</h1>",
		NetworkSummary: &browser.NetworkSummary{
			TotalRequests: 10,
		},
		DOMSignals: &browser.DOMSignals{},
	}

	banner := buildDiagnosticBanner(result)
	if banner != "" {
		t.Errorf("clean scrape should produce no banner; got:\n%s", banner)
	}
}

// TestBuildDiagnosticBannerAuthFailure reproduces the #87 scenario: requested
// a protected page, got redirected to login with 401s in the network log.
func TestBuildDiagnosticBannerAuthFailure(t *testing.T) {
	result := &Result{
		URL:      "https://my.rebrainme.com/course/nexus/task/3706",
		FinalURL: "https://my.rebrainme.com/",
		HTML:     "# Вход\nE-mail или телефон\nПароль",
		NetworkSummary: &browser.NetworkSummary{
			TotalRequests: 179,
			AuthFailures: []browser.AuthFailure{
				{URL: "https://my.rebrainme.com/api/v2/tasks/3706", Status: 401},
				{URL: "https://my.rebrainme.com/api/v2/courses/nexus/menu", Status: 401},
			},
		},
		DOMSignals: &browser.DOMSignals{HasLoginForm: true},
	}

	banner := buildDiagnosticBanner(result)
	if banner == "" {
		t.Fatal("auth failure should produce a banner")
	}

	// Banner must mention the redirect.
	if !strings.Contains(banner, "my.rebrainme.com/course/nexus/task/3706") {
		t.Errorf("banner should mention requested URL; got:\n%s", banner)
	}
	if !strings.Contains(banner, "my.rebrainme.com/") {
		t.Errorf("banner should mention final URL; got:\n%s", banner)
	}

	// Banner must mention the 401 auth failures.
	if !strings.Contains(banner, "401") {
		t.Errorf("banner should mention 401 status; got:\n%s", banner)
	}

	// Banner must tell the LLM NOT to blindly retry.
	if !strings.Contains(banner, "Do NOT blindly retry") {
		t.Errorf("banner should warn against blind retry; got:\n%s", banner)
	}

	// Banner must suggest a login action.
	if !strings.Contains(banner, "login") {
		t.Errorf("banner should suggest a login action; got:\n%s", banner)
	}

	// Banner must start with the alert marker.
	if !strings.HasPrefix(banner, "⚠️ DIAGNOSTIC ALERT") {
		t.Errorf("banner should start with DIAGNOSTIC ALERT; got:\n%s", banner[:min(len(banner), 50)])
	}
}
func TestBuildDiagnosticBannerBlocked(t *testing.T) {
	result := &Result{
		URL:      "https://example.com/protected",
		FinalURL: "https://example.com/protected",
		HTML:     "Checking your browser...",
		DOMSignals: &browser.DOMSignals{
			BlockedHint: "cloudflare",
		},
	}

	banner := buildDiagnosticBanner(result)
	if banner == "" {
		t.Fatal("blocking should produce a banner")
	}
	if !strings.Contains(banner, "cloudflare") {
		t.Errorf("banner should mention cloudflare; got:\n%s", banner)
	}
	if !strings.Contains(banner, "stealth") {
		t.Errorf("banner should suggest stealth; got:\n%s", banner)
	}
}

// TestBuildDiagnosticBannerFallback verifies the HTTP-fallback path.
func TestBuildDiagnosticBannerFallback(t *testing.T) {
	result := &Result{
		URL:             "https://example.com/page",
		FinalURL:        "https://example.com/page",
		HTML:            "<html>static content</html>",
		FallbackReason:  "chrome_action_error",
		FallbackWarning: "HTTP fallback returned only 51 bytes",
	}

	banner := buildDiagnosticBanner(result)
	if banner == "" {
		t.Fatal("fallback should produce a banner")
	}
	if !strings.Contains(banner, "chrome_action_error") {
		t.Errorf("banner should mention fallback reason; got:\n%s", banner)
	}
	if !strings.Contains(banner, "51 bytes") {
		t.Errorf("banner should include fallback warning; got:\n%s", banner)
	}
}

// TestBuildDiagnosticBannerNilSafe verifies the function is nil-safe.
func TestBuildDiagnosticBannerNilSafe(t *testing.T) {
	if banner := buildDiagnosticBanner(nil); banner != "" {
		t.Errorf("nil result should produce no banner; got %q", banner)
	}
}

// TestBuildDiagnosticBannerLoginFormNoRedirect covers a login form present on
// the requested page itself (no redirect) — still signals auth needed.
func TestBuildDiagnosticBannerLoginFormNoRedirect(t *testing.T) {
	result := &Result{
		URL:      "https://example.com/login",
		FinalURL: "https://example.com/login",
		HTML:     "<form><input type='password'></form>",
		DOMSignals: &browser.DOMSignals{
			HasLoginForm: true,
		},
	}

	banner := buildDiagnosticBanner(result)
	if banner == "" {
		t.Fatal("login form (no redirect) should produce a banner")
	}
	if !strings.Contains(banner, "Login form detected") {
		t.Errorf("banner should mention login form; got:\n%s", banner)
	}
}
