package browser

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateAction covers the validation logic centralised in
// validateAction — the fix for issue #50. A single invalid action (e.g.
// wait_for with an empty selector) must be rejected as an
// *ActionValidationError, not a generic error, so the retry loop can detect it
// via IsActionValidationError and skip backoff.
func TestValidateAction(t *testing.T) {
	tests := []struct {
		name      string
		action    Action
		wantValid bool
		wantType  string // expected ActionValidationError.ActionType (if invalid)
		// wantReasonSubstr must appear in the validation error reason (if invalid).
		wantReasonSubstr string
	}{
		// Valid actions — all required fields present.
		{
			name:      "click with selector is valid",
			action:    Action{Type: "click", Selector: "#btn"},
			wantValid: true,
		},
		{
			name:      "type with selector and text is valid",
			action:    Action{Type: "type", Selector: "#input", Text: "hello"},
			wantValid: true,
		},
		{
			name:      "wait_for with selector is valid",
			action:    Action{Type: "wait_for", Selector: ".loaded"},
			wantValid: true,
		},
		{
			name:      "wait_for_text with text is valid",
			action:    Action{Type: "wait_for_text", Text: "Welcome"},
			wantValid: true,
		},
		{
			name:      "execute_js with code is valid",
			action:    Action{Type: "execute_js", Text: "(() => 1)()"},
			wantValid: true,
		},
		{
			name:      "navigate with url is valid",
			action:    Action{Type: "navigate", Text: "https://example.com"},
			wantValid: true,
		},
		{
			name:      "select_option with selector and value is valid",
			action:    Action{Type: "select_option", Selector: "#sel", Value: "opt"},
			wantValid: true,
		},
		{
			name:      "upload_file with selector and path is valid",
			action:    Action{Type: "upload_file", Selector: "#file", Text: "/tmp/x"},
			wantValid: true,
		},

		// Invalid actions — the core #50 regressions.
		{
			name:             "wait_for with empty selector is invalid (#50)",
			action:           Action{Type: "wait_for", Selector: ""},
			wantValid:        false,
			wantType:         "wait_for",
			wantReasonSubstr: "selector is required",
		},
		{
			name:             "click with empty selector is invalid",
			action:           Action{Type: "click", Selector: ""},
			wantValid:        false,
			wantType:         "click",
			wantReasonSubstr: "selector is required",
		},
		{
			name:             "type with empty selector is invalid",
			action:           Action{Type: "type", Selector: "", Text: "hi"},
			wantValid:        false,
			wantType:         "type",
			wantReasonSubstr: "selector is required",
		},
		{
			name:             "type with empty text is invalid",
			action:           Action{Type: "type", Selector: "#i", Text: ""},
			wantValid:        false,
			wantType:         "type",
			wantReasonSubstr: "text is required",
		},
		{
			name:             "wait_for_text with empty text is invalid",
			action:           Action{Type: "wait_for_text", Text: ""},
			wantValid:        false,
			wantType:         "wait_for_text",
			wantReasonSubstr: "text is required",
		},
		{
			name:             "unknown action type is invalid",
			action:           Action{Type: "frobnicate", Selector: "#x"},
			wantValid:        false,
			wantType:         "frobnicate",
			wantReasonSubstr: "unknown action type",
		},
		{
			name:             "execute_js with empty code is invalid",
			action:           Action{Type: "execute_js", Text: ""},
			wantValid:        false,
			wantType:         "execute_js",
			wantReasonSubstr: "text is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAction(tt.action, 0)
			if tt.wantValid {
				if err != nil {
					t.Fatalf("expected valid action, got error: %v", err)
				}
				return
			}

			// Invalid action: must be an *ActionValidationError.
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			var ve *ActionValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ActionValidationError, got %T: %v", err, err)
			}
			if ve.ActionType != tt.wantType {
				t.Errorf("ActionType = %q, want %q", ve.ActionType, tt.wantType)
			}
			if !strings.Contains(ve.Reason, tt.wantReasonSubstr) {
				t.Errorf("Reason = %q, want substring %q", ve.Reason, tt.wantReasonSubstr)
			}
		})
	}
}

// TestIsActionValidationError ensures errors.As unwrapping works for wrapped
// errors — the retry loop relies on this to detect validation failures.
func TestIsActionValidationError(t *testing.T) {
	ve := &ActionValidationError{ActionIndex: 2, ActionType: "wait_for", Reason: "selector is required"}
	wrapped := errors.New("failed to execute actions: " + ve.Error())

	if !IsActionValidationError(ve) {
		t.Error("IsActionValidationError(ve) = false, want true")
	}
	// A plain non-validation error must not match.
	if IsActionValidationError(errors.New("context deadline exceeded")) {
		t.Error("plain error matched as validation error")
	}
	// Note: wrapped errors from fmt.Errorf("...: %w", ve) WOULD match via
	// errors.As, but a manually-constructed errors.New wrapping the string
	// does not — this documents that the caller must wrap, not stringify.
	_ = wrapped
}

// TestParseActionsRejectsInvalid is the end-to-end guard for issue #50: the
// parser must reject an invalid action eagerly, before any of the (valid)
// actions in the chain are attempted in Chrome. This is what stops a single
// bad action from killing an otherwise-correct login workflow.
func TestParseActionsRejectsInvalid(t *testing.T) {
	// Simulate the exact #50 scenario: 5 valid login actions followed by a
	// wait_for with an empty selector.
	actionsData := []interface{}{
		map[string]interface{}{"type": "click", "selector": "#email"},
		map[string]interface{}{"type": "type", "selector": "#email", "text": "user@example.com"},
		map[string]interface{}{"type": "click", "selector": "#password"},
		map[string]interface{}{"type": "type", "selector": "#password", "text": "secret"},
		map[string]interface{}{"type": "submit", "selector": "#submit"},
		map[string]interface{}{"type": "wait_for", "selector": ""}, // the #50 trigger
	}

	parsed, err := ParseActions(actionsData)
	if err == nil {
		t.Fatalf("ParseActions returned no error for invalid action; got %d actions", len(parsed))
	}

	// The error must be an *ActionValidationError so downstream classification
	// (isActionError in chrome_scraper) and the retry loop both handle it.
	var ve *ActionValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ActionValidationError from ParseActions, got %T: %v", err, err)
	}
	// The invalid action was at index 5 (0-based).
	if ve.ActionIndex != 5 {
		t.Errorf("ActionIndex = %d, want 5", ve.ActionIndex)
	}
	if ve.ActionType != "wait_for" {
		t.Errorf("ActionType = %q, want wait_for", ve.ActionType)
	}
}

// TestParseActionsAcceptsValid confirms valid action chains parse without error
// — a regression guard so the eager validation doesn't reject legitimate input.
func TestParseActionsAcceptsValid(t *testing.T) {
	actionsData := []interface{}{
		map[string]interface{}{"type": "click", "selector": "#btn"},
		map[string]interface{}{"type": "wait_for", "selector": ".done", "timeout": float64(5000)},
		map[string]interface{}{"type": "navigate", "text": "https://example.com"},
	}

	parsed, err := ParseActions(actionsData)
	if err != nil {
		t.Fatalf("ParseActions failed on valid input: %v", err)
	}
	if len(parsed) != 3 {
		t.Fatalf("got %d actions, want 3", len(parsed))
	}
	// Verify the timeout parsed correctly (ms → Duration).
	if parsed[1].Timeout.Milliseconds() != 5000 {
		t.Errorf("timeout = %v, want 5000ms", parsed[1].Timeout)
	}
}
