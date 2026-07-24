package tools

import (
	"sort"
	"strings"
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
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
