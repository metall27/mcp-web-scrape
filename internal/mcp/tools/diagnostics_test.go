package tools

import (
	"encoding/json"
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// TestScrapeErrorPhaseDiagnostics verifies the #77 extensions: a ScrapeError
// carries Phase + Diagnostics, and they survive a JSON round-trip so they can
// be surfaced to the LLM in the error response.
func TestScrapeErrorPhaseDiagnostics(t *testing.T) {
	ns := browser.NetworkSummary{
		TotalRequests: 5,
		AuthFailures:  []browser.AuthFailure{{URL: "https://x.com/api/tasks", Status: 401}},
	}
	ds := browser.DOMSignals{HasLoginForm: true, IsSPA: true, Framework: "react"}

	err := &ScrapeError{
		Code:    "auth_required",
		Message: "page returned 401 after navigation",
		Hints:   []string{"provide_credentials"},
		Phase:   "classify",
		Diagnostics: &PageDiagnostics{
			URL:            "https://x.com/dashboard",
			Title:          "Login",
			HTTPStatusCode: 200,
			NetworkSummary: &ns,
			DOMSignals:     &ds,
			AttemptedSteps: []string{"navigate", "settle", "classify"},
			Hypothesis: &Hypothesis{
				Cause:  "auth_required",
				Detail: "page has a login form and /api/tasks returned 401",
				Evidence: []string{
					"POST /api/tasks → 401",
					"has_login_form: true",
				},
			},
			Suggestions: []Suggestion{
				{Action: "provide_credentials", Detail: "supply login form selector + credentials via actions"},
			},
		},
	}

	// Error() still returns just the message (unchanged contract).
	if got := err.Error(); got != "page returned 401 after navigation" {
		t.Errorf("Error() = %q, want message only", got)
	}

	// The diagnostics survive JSON serialization with all nested fields.
	b, err2 := json.Marshal(err)
	if err2 != nil {
		t.Fatalf("marshal: %v", err2)
	}
	j := string(b)
	for _, want := range []string{
		`"phase":"classify"`,
		`"cause":"auth_required"`,
		`"has_login_form":true`,
		`"auth_required"`,
		`"provide_credentials"`,
		`"navigate"`,
	} {
		if !containsStr(j, want) {
			t.Errorf("ScrapeError JSON missing %q; got %s", want, j)
		}
	}
}

// TestScrapeErrorSimpleStillWorks verifies that a simple transport error
// (no Phase, no Diagnostics) still serializes cleanly — backward compatible.
func TestScrapeErrorSimpleStillWorks(t *testing.T) {
	err := &ScrapeError{
		Code:    "timeout",
		Message: "context deadline exceeded",
	}
	b, e := json.Marshal(err)
	if e != nil {
		t.Fatalf("marshal: %v", e)
	}
	j := string(b)
	// Diagnostics and Phase are nil/empty → omitted by omitempty.
	for _, absent := range []string{"diagnostics", "phase", "hypothesis"} {
		if containsStr(j, absent) {
			t.Errorf("simple ScrapeError should not contain %q; got %s", absent, j)
		}
	}
	if !containsStr(j, `"code":"timeout"`) {
		t.Errorf("simple ScrapeError missing code; got %s", j)
	}
}

// TestPageDiagnosticsOmitempty verifies all fields are omitempty so a sparse
// diagnostics payload stays compact — no noise for the LLM.
func TestPageDiagnosticsOmitempty(t *testing.T) {
	d := PageDiagnostics{}
	b, _ := json.Marshal(d)
	if s := string(b); s != "{}" {
		t.Errorf("empty PageDiagnostics = %s, want {} (all omitempty)", s)
	}
}

// containsStr is a local helper to avoid depending on strings import in this
// test file (matches the style in network_monitor_test.go).
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && indexOfStr(s, sub) >= 0
}

func indexOfStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
