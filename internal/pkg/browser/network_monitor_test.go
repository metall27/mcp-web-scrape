package browser

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

// TestNetworkMonitorAuthFailures verifies the core #77/#71 signal: 401/403
// responses are captured as AuthFailures. This is what lets the LLM diagnose
// "this page requires auth" without guessing from an empty body.
func TestNetworkMonitorAuthFailures(t *testing.T) {
	m := NewNetworkMonitor()

	// Simulate CDP events directly — recordResponse is the ListenTarget handler.
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/api/v2/tasks/3704", Status: 401},
	})
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/api/v2/auth", Status: 200},
	})
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/api/v2/forbidden", Status: 403},
	})
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/ok", Status: 200},
	})

	s := m.Summary()
	if len(s.AuthFailures) != 2 {
		t.Fatalf("AuthFailures = %d, want 2 (401+403)", len(s.AuthFailures))
	}
	statuses := map[int]bool{}
	for _, af := range s.AuthFailures {
		statuses[af.Status] = true
	}
	if !statuses[401] || !statuses[403] {
		t.Errorf("expected both 401 and 403 in auth failures, got %v", statuses)
	}
}

// TestNetworkMonitorCDNBlocking verifies 429/503 flag the BlockedByCDN heuristic.
func TestNetworkMonitorCDNBlocking(t *testing.T) {
	m := NewNetworkMonitor()
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/", Status: 429},
	})
	if s := m.Summary(); !s.BlockedByCDN {
		t.Error("429 response should flag BlockedByCDN")
	}

	m2 := NewNetworkMonitor()
	m2.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/", Status: 503},
	})
	if s := m2.Summary(); !s.BlockedByCDN {
		t.Error("503 response should flag BlockedByCDN")
	}
}

// TestNetworkMonitorSummaryIsCopy verifies Summary returns a defensive copy —
// callers must not be able to mutate the monitor's internal state via the
// returned slice.
func TestNetworkMonitorSummaryIsCopy(t *testing.T) {
	m := NewNetworkMonitor()
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/a", Status: 401},
	})

	s1 := m.Summary()
	s1.AuthFailures[0] = AuthFailure{URL: "MUTATED", Status: 999}

	s2 := m.Summary()
	if s2.AuthFailures[0].Status == 999 {
		t.Error("Summary returned a slice aliasing internal state; expected a copy")
	}
}

// TestNetworkMonitorRequestCap verifies the request log is bounded to
// maxRecordedRequests, so a request-heavy page can't blow up metadata.
func TestNetworkMonitorRequestCap(t *testing.T) {
	m := NewNetworkMonitor()
	for i := 0; i < maxRecordedRequests+50; i++ {
		m.recordResponse(&network.EventResponseReceived{
			Response: &network.Response{URL: "https://x.com/r", Status: 200},
		})
	}
	s := m.Summary()
	if len(s.Requests) > maxRecordedRequests {
		t.Errorf("Requests len = %d, want <= %d", len(s.Requests), maxRecordedRequests)
	}
}

// TestNetworkMonitorAuthFailureHint verifies the compact human-readable hint
// format the LLM sees in metadata.network_summary.auth_hint.
func TestNetworkMonitorAuthFailureHint(t *testing.T) {
	// No failures → empty hint.
	m := NewNetworkMonitor()
	if h := m.Summary().AuthFailureHint(); h != "" {
		t.Errorf("empty AuthFailureHint = %q, want empty", h)
	}

	// With failures → trimmed paths + status codes.
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/api/v2/tasks/3704", Status: 401},
	})
	m.recordResponse(&network.EventResponseReceived{
		Response: &network.Response{URL: "https://x.com/api/v2/menu", Status: 403},
	})
	h := m.Summary().AuthFailureHint()
	if h == "" {
		t.Fatal("expected non-empty AuthFailureHint")
	}
	if !contains(h, "401") || !contains(h, "403") {
		t.Errorf("AuthFailureHint %q should contain both 401 and 403", h)
	}
	// Should contain trimmed paths, not full URLs.
	if !contains(h, "/api/v2/tasks/3704") {
		t.Errorf("AuthFailureHint %q should contain the trimmed path", h)
	}
}

// TestNetworkSummaryJSON verifies the documented JSON keys are present so
// downstream consumers (LLM clients) can rely on them.
func TestNetworkSummaryJSON(t *testing.T) {
	s := NetworkSummary{
		TotalRequests: 42,
		BlockedByCDN:  true,
		AuthFailures:  []AuthFailure{{URL: "https://x.com/api", Status: 401}},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j := string(b)
	for _, key := range []string{"total_requests", "blocked_by_cdn", "auth_failures"} {
		if !contains(j, key) {
			t.Errorf("NetworkSummary JSON missing key %q; got %s", key, j)
		}
	}
}

// contains is a small helper to avoid importing strings in this test file.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// jsonMarshal removed — tests import encoding/json directly.

// TestNetworkMonitorIsIdle verifies the idle check logic without a live CDP
// session (we can't drive a real browser in a unit test). We only assert the
// empty-monitor case — a fresh monitor with no activity reports idle.
func TestNetworkMonitorIsIdleEmpty(t *testing.T) {
	m := NewNetworkMonitor()
	// No activity yet → lastActivityAt == 0 → IsIdle returns true.
	if !m.IsIdle(time.Second) {
		t.Error("fresh monitor with no activity should be idle")
	}
}

// TestNetworkMonitorStartIdempotent verifies Start can be called twice without
// re-registering the listener (the started guard).
func TestNetworkMonitorStartIdempotent(t *testing.T) {
	m := NewNetworkMonitor()
	// We can't call Start without a real CDP context, but we can verify the
	// idempotency guard logic: mark started and call Start again — it should
	// return nil immediately without touching CDP.
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	if err := m.Start(nil); err != nil {
		t.Errorf("second Start should be a no-op returning nil; got %v", err)
	}
}
