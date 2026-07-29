package tools

import (
	"context"
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// TestDetectContentCandidates_FiresForRebrainmePattern simulates the exact
// rebrainme scenario from #83: a SPA with a large JSON API response (87KB) but
// a thin rendered DOM (82KB shell). The 3x ratio threshold should fire.
func TestDetectContentCandidates_FiresForRebrainmePattern(t *testing.T) {
	m := browser.NewNetworkMonitor()

	// Simulate the rebrainme API response: 87KB JSON via XHR.
	m.RecordResponseForTest("req-1", "https://x.com/api/v2/tasks/3706",
		200, "application/json", "XHR", 87206)

	// The rendered HTML is an 82KB shell (thin content).
	html := make([]byte, 82000) // 82KB

	signals := &browser.DOMSignals{IsSPA: true, Framework: "react"}
	candidates := detectContentCandidates(context.Background(), m, signals, string(html))

	if len(candidates) != 1 {
		t.Fatalf("expected 1 content candidate, got %d", len(candidates))
	}
	if candidates[0].URL != "https://x.com/api/v2/tasks/3706" {
		t.Errorf("candidate URL = %q, want API URL", candidates[0].URL)
	}
}

// TestDetectContentCandidates_NoFireForSmallAPI verifies that a SPA with a
// small API response (below the 3x ratio) does NOT trigger detection.
// This prevents false positives on pages that make modest API calls alongside
// a full DOM render.
func TestDetectContentCandidates_NoFireForSmallAPI(t *testing.T) {
	m := browser.NewNetworkMonitor()

	// Small config API: 5KB JSON.
	m.RecordResponseForTest("req-1", "https://x.com/api/config",
		200, "application/json", "XHR", 5000)

	// Full HTML render: 80KB (ratio = 5/80 = 0.0625, well below 3x).
	html := make([]byte, 80000)

	signals := &browser.DOMSignals{IsSPA: true}
	candidates := detectContentCandidates(context.Background(), m, signals, string(html))

	if candidates != nil {
		t.Errorf("expected nil candidates for small API response, got %d", len(candidates))
	}
}

// TestDetectContentCandidates_NoFireForNonSPA verifies that detection only
// runs for SPA pages. A static HTML page with large JSON (e.g. a blog with
// embedded data) should not trigger.
func TestDetectContentCandidates_NoFireForNonSPA(t *testing.T) {
	m := browser.NewNetworkMonitor()

	// Large JSON, but page is NOT a SPA.
	m.RecordResponseForTest("req-1", "https://x.com/api/data",
		200, "application/json", "XHR", 100000)

	html := make([]byte, 5000)

	signals := &browser.DOMSignals{IsSPA: false}
	candidates := detectContentCandidates(context.Background(), m, signals, string(html))

	if candidates != nil {
		t.Errorf("expected nil candidates for non-SPA page, got %d", len(candidates))
	}
}

// TestDetectContentCandidates_MultipleCandidates verifies that multiple large
// API responses are all captured when the ratio threshold is exceeded.
func TestDetectContentCandidates_MultipleCandidates(t *testing.T) {
	m := browser.NewNetworkMonitor()

	m.RecordResponseForTest("req-1", "https://x.com/api/tasks",
		200, "application/json", "XHR", 50000)
	m.RecordResponseForTest("req-2", "https://x.com/api/menu",
		200, "application/json", "Fetch", 30000)

	// Total API = 80KB, HTML = 10KB → ratio = 8x (> 3x threshold).
	html := make([]byte, 10000)

	signals := &browser.DOMSignals{IsSPA: true}
	candidates := detectContentCandidates(context.Background(), m, signals, string(html))

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

// TestDetectContentCandidates_LargeAPI verifies edge case: even with a thin
// signal (empty/nil HTML), a 10KB+ API response still triggers detection.
func TestDetectContentCandidates_LargeAPI(t *testing.T) {
	m := browser.NewNetworkMonitor()

	m.RecordResponseForTest("req-1", "https://x.com/api/data",
		200, "application/json", "XHR", 15000)

	signals := &browser.DOMSignals{IsSPA: true}
	candidates := detectContentCandidates(context.Background(), m, signals, "")

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate for large API + empty HTML, got %d", len(candidates))
	}
}

// TestDetectContentCandidates_NilSignals verifies graceful handling of nil
// signals — the function should return nil, not panic.
func TestDetectContentCandidates_NilSignals(t *testing.T) {
	m := browser.NewNetworkMonitor()
	m.RecordResponseForTest("req-1", "https://x.com/api/data",
		200, "application/json", "XHR", 5000)

	candidates := detectContentCandidates(context.Background(), m, nil, "some html")
	if candidates != nil {
		t.Errorf("expected nil candidates for nil signals")
	}
}
