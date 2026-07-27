package browser

import (
	"encoding/json"
	"testing"
	"time"
)

// TestDefaultSettleConfig guards the defaults documented in settle.go: the
// default config must be non-zero across all timing/length fields so SmartSettle
// behaves predictably when called with a zero-value SettleConfig. A regression
// here (e.g. someone zeroes out MaxWait) would make SmartSettle hang forever or
// return instantly without settling.
func TestDefaultSettleConfig(t *testing.T) {
	cfg := DefaultSettleConfig()
	if cfg.MaxWait <= 0 {
		t.Errorf("MaxWait = %v, want > 0", cfg.MaxWait)
	}
	if cfg.MinContentLen <= 0 {
		t.Errorf("MinContentLen = %v, want > 0", cfg.MinContentLen)
	}
	if cfg.StabilityWindow <= 0 {
		t.Errorf("StabilityWindow = %v, want > 0", cfg.StabilityWindow)
	}
	if cfg.PollInterval <= 0 {
		t.Errorf("PollInterval = %v, want > 0", cfg.PollInterval)
	}
	// The documented ~5s cap.
	if cfg.MaxWait != 5*time.Second {
		t.Errorf("MaxWait = %v, want 5s", cfg.MaxWait)
	}
	// PollInterval must be < StabilityWindow, otherwise stabilization can never
	// be observed (we'd never see two identical consecutive hashes).
	if cfg.PollInterval >= cfg.StabilityWindow {
		t.Errorf("PollInterval (%v) must be < StabilityWindow (%v)",
			cfg.PollInterval, cfg.StabilityWindow)
	}
	// MinContentLen must be small enough that a minimal real page passes but
	// large enough to reject an empty <body></body> shell. 50 is the documented
	// default; a non-SPA static page is well above this.
	if cfg.MinContentLen != 50 {
		t.Errorf("MinContentLen = %v, want 50", cfg.MinContentLen)
	}
}

// TestSettleReportJSON ensures SettleReport serializes with the documented JSON
// keys (phase, emerged_ms, total_ms). These keys appear in action_observations
// metadata consumed by LLM clients; renaming them silently breaks consumers.
func TestSettleReportJSON(t *testing.T) {
	r := SettleReport{
		Phase:     "stable",
		EmergedMs: 123,
		TotalMs:   456,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"phase", "emerged_ms", "total_ms"} {
		if _, ok := m[key]; !ok {
			t.Errorf("SettleReport JSON missing key %q; got %s", key, string(b))
		}
	}
	if m["phase"] != "stable" {
		t.Errorf("phase = %v, want stable", m["phase"])
	}
}

// TestAuthSignalsJSON ensures AuthSignals serializes with the documented JSON
// keys (cookies, auth_keys) and that it never includes token VALUES — only key
// names. This is the privacy invariant: credentials must not leak into scrape
// metadata.
func TestAuthSignalsJSON(t *testing.T) {
	a := AuthSignals{
		Cookies:  71,
		AuthKeys: []string{"auth-store", "jwt_token"},
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["cookies"]; !ok {
		t.Errorf("AuthSignals JSON missing key %q; got %s", "cookies", string(b))
	}
	if _, ok := m["auth_keys"]; !ok {
		t.Errorf("AuthSignals JSON missing key %q; got %s", "auth_keys", string(b))
	}
	// Sanity: no value fields present (we deliberately only store counts/names).
	for _, forbidden := range []string{"token", "value", "secret", "cookie_value"} {
		if _, ok := m[forbidden]; ok {
			t.Errorf("AuthSignals must not expose %q (credential leak risk)", forbidden)
		}
	}
}
