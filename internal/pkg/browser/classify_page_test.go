package browser

import (
	"encoding/json"
	"testing"
)

// TestDOMSignalsJSONOmitempty verifies the omitempty tags: a zero-value
// DOMSignals (no signals fired) serializes to an empty JSON object, so
// metadata stays clean when the page matched no heuristics. A populated one
// emits exactly the fired fields.
func TestDOMSignalsJSONOmitempty(t *testing.T) {
	// Empty: no signal → {} (all fields omitted).
	empty := DOMSignals{}
	b, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if s := string(b); s != "{}" {
		t.Errorf("empty DOMSignals = %s, want {} (all omitempty)", s)
	}

	// All signals fired.
	full := DOMSignals{
		HasLoginForm: true,
		IsSPA:        true,
		Framework:    "next",
		BlockedHint:  "cloudflare",
	}
	b, err = json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal full: %v", err)
	}
	j := string(b)
	for _, key := range []string{`"has_login_form":true`, `"is_spa":true`, `"framework":"next"`, `"blocked_hint":"cloudflare"`} {
		if !contains(j, key) {
			t.Errorf("full DOMSignals JSON missing %q; got %s", key, j)
		}
	}

	// Partial: only login form.
	partial := DOMSignals{HasLoginForm: true}
	b, _ = json.Marshal(partial)
	j = string(b)
	if !contains(j, `"has_login_form":true`) {
		t.Errorf("partial DOMSignals missing login flag; got %s", j)
	}
	// is_spa/framework/blocked_hint must NOT appear when zero.
	for _, absent := range []string{"is_spa", "framework", "blocked_hint"} {
		if contains(j, absent) {
			t.Errorf("partial DOMSignals should NOT contain %q; got %s", absent, j)
		}
	}
}

// TestDOMSignalsExpectedKeys verifies the JSON keys match what js_tool.go
// surfaces in metadata.dom_signals — the LLM-facing contract.
func TestDOMSignalsExpectedKeys(t *testing.T) {
	// Unmarshal into a generic map to confirm the exact key names the LLM sees.
	ds := DOMSignals{HasLoginForm: true, IsSPA: true, Framework: "react", BlockedHint: "recaptcha"}
	b, _ := json.Marshal(ds)
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	expected := map[string]bool{
		"has_login_form": false,
		"is_spa":         false,
		"framework":      false,
		"blocked_hint":   false,
	}
	for k := range m {
		if _, ok := expected[k]; ok {
			expected[k] = true
		}
	}
	for k, seen := range expected {
		if !seen {
			t.Errorf("expected key %q missing from DOMSignals JSON; got %v", k, m)
		}
	}
}
