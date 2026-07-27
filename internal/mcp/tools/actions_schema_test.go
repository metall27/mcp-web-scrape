package tools

import (
	"sort"
	"strings"
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

// schemaRequiredFields extracts the required fields for each action type from
// the oneOf branches in buildActionsSchema(). Returns a map: actionType → set
// of required field names (excluding "type", which every branch requires).
func schemaRequiredFields(t *testing.T) map[string]map[string]bool {
	t.Helper()

	schema := buildActionsSchema()
	items, ok := schema["items"].(map[string]interface{})
	if !ok {
		t.Fatal("schema items is not an object")
	}
	oneOf, ok := items["oneOf"].([]interface{})
	if !ok {
		t.Fatal("schema items has no oneOf array")
	}

	result := make(map[string]map[string]bool)
	for i, branch := range oneOf {
		b, ok := branch.(map[string]interface{})
		if !ok {
			t.Fatalf("oneOf branch %d is not an object", i)
		}
		props, ok := b["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("oneOf branch %d has no properties", i)
		}
		typeProp, ok := props["type"].(map[string]interface{})
		if !ok {
			t.Fatalf("oneOf branch %d has no type property", i)
		}
		enum, ok := typeProp["enum"].([]string)
		if !ok || len(enum) != 1 {
			t.Fatalf("oneOf branch %d type enum must have exactly one value", i)
		}
		actionType := enum[0]

		required, _ := b["required"].([]string)
		fieldSet := make(map[string]bool)
		for _, r := range required {
			if r != "type" {
				fieldSet[r] = true
			}
		}
		result[actionType] = fieldSet
	}
	return result
}

// validatorRequiredFields mirrors browser.requiredFields without exporting
// it. It returns the same map: actionType → set of required field names.
func validatorRequiredFields() map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for _, rf := range browser.RequiredFields() {
		fieldSet := make(map[string]bool)
		for _, f := range rf.Fields {
			fieldSet[f] = true
		}
		result[rf.ActionType] = fieldSet
	}
	return result
}

// TestActionsSchemaMatchesValidator is the core guard for issue #55: the
// JSON-schema advertised to LLM clients (via tools/list) must encode the same
// required-field rules that ParseActions/validateAction enforce in
// browser/actions.go. If they drift, agents will keep sending malformed
// actions (e.g. {type:"wait_for"} with no selector) that the server rejects.
func TestActionsSchemaMatchesValidator(t *testing.T) {
	schemaFields := schemaRequiredFields(t)
	validatorFields := validatorRequiredFields()

	allTypes := make(map[string]bool)
	for k := range schemaFields {
		allTypes[k] = true
	}
	for k := range validatorFields {
		allTypes[k] = true
	}

	var mismatches []string
	for actionType := range allTypes {
		sf, sinSchema := schemaFields[actionType]
		vf, inValidator := validatorFields[actionType]
		if !sinSchema {
			mismatches = append(mismatches, actionType+": missing from schema oneOf")
			continue
		}
		if !inValidator {
			mismatches = append(mismatches, actionType+": missing from validator requiredFields")
			continue
		}
		if !sameSet(sf, vf) {
			mismatches = append(mismatches,
				actionType+": schema requires "+setStr(sf)+
					" but validator requires "+setStr(vf))
		}
	}

	if len(mismatches) > 0 {
		sort.Strings(mismatches)
		t.Fatalf("schema/validator mismatch for action types:\n  %s",
			strings.Join(mismatches, "\n  "))
	}
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func setStr(s map[string]bool) string {
	var parts []string
	for k := range s {
		parts = append(parts, k)
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

// TestScrapeJSSchemaHasObserveChanges guards issue #72: the scrape_with_js
// tool schema must advertise the observe_changes parameter and it must default
// to false. A missing or mis-defaulted entry means the LLM never learns the
// feedback-loop feature exists.
func TestScrapeJSSchemaHasObserveChanges(t *testing.T) {
	// Re-invoke the constructor used for registration so we inspect the exact
	// schema clients receive, not a hand-maintained copy.
	tool := newSchemaOnlyScrapeJSTool(t)
	props, ok := tool.InputSchema()["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema has no properties object")
	}
	oc, ok := props["observe_changes"].(map[string]interface{})
	if !ok {
		t.Fatal("observe_changes missing from scrape_with_js schema properties")
	}
	if oc["type"] != "boolean" {
		t.Errorf("observe_changes.type = %v, want boolean", oc["type"])
	}
	if oc["default"] != false {
		t.Errorf("observe_changes.default = %v, want false", oc["default"])
	}
	desc, _ := oc["description"].(string)
	if !strings.Contains(desc, "action_observations") {
		t.Errorf("observe_changes description does not mention action_observations metadata key; got %q", desc)
	}
}

// TestScrapeJSSchemaHasSmartSettle guards issue #71: the scrape_with_js tool
// schema must advertise the smart_settle parameter and it must default to true.
// smart_settle is the post-action DOM-stability wait that solves empty-content
// snapshots after navigate/click on SPAs — a missing or mis-defaulted entry
// means the LLM never learns the feature exists and falls back to stale behavior.
func TestScrapeJSSchemaHasSmartSettle(t *testing.T) {
	tool := newSchemaOnlyScrapeJSTool(t)
	props, ok := tool.InputSchema()["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema has no properties object")
	}
	ss, ok := props["smart_settle"].(map[string]interface{})
	if !ok {
		t.Fatal("smart_settle missing from scrape_with_js schema properties")
	}
	if ss["type"] != "boolean" {
		t.Errorf("smart_settle.type = %v, want boolean", ss["type"])
	}
	if ss["default"] != true {
		t.Errorf("smart_settle.default = %v, want true", ss["default"])
	}
	desc, _ := ss["description"].(string)
	if !strings.Contains(desc, "settle") {
		t.Errorf("smart_settle description does not mention settle; got %q", desc)
	}
}

// TestActionsSchemaIncludesNewWaitActions guards issue #71: the schema must
// advertise wait_for_navigation and wait_for_content — the escape-hatch wait
// actions for SPA login/navigation flows. Without them the LLM has no way to
// explicitly wait for URL change or content emergence.
func TestActionsSchemaIncludesNewWaitActions(t *testing.T) {
	schemaFields := schemaRequiredFields(t)
	for _, actionType := range []string{"wait_for_navigation", "wait_for_content"} {
		if _, ok := schemaFields[actionType]; !ok {
			t.Errorf("action type %q missing from schema oneOf (issue #71)", actionType)
			continue
		}
		// Both are field-less wait actions: no selector/text required.
		if len(schemaFields[actionType]) != 0 {
			t.Errorf("action %q should require no fields beyond type; got %v",
				actionType, schemaFields[actionType])
		}
	}
}

// newSchemaOnlyScrapeJSTool returns a ScrapeJSTool whose schema is populated.
// We cannot call NewScrapeJSTool (it needs a browser pool); instead replicate
// the schema map via buildActionsSchema + the known property set by invoking
// the public constructor path minimally.
func newSchemaOnlyScrapeJSTool(t *testing.T) *ScrapeJSTool {
	t.Helper()
	// Construct with nil deps — the schema is static and does not touch them.
	// NewScrapeJSTool wires a handler that uses the pool at call time only.
	return NewScrapeJSTool(nil, nil, config.RAGConfig{}, config.BrowserConfig{}, nil, nil, config.GitHubConfig{})
}
