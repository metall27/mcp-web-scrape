package browser

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractReportJSON verifies the metadata-facing JSON shape of ExtractReport
// — the contract js_tool.go surfaces in metadata.extract_report.
func TestExtractReportJSON(t *testing.T) {
	// Full report with missing + warnings.
	er := ExtractReport{
		Records:  10,
		Fields:   3,
		Missing:  []string{"sku"},
		Warnings: []string{"price: selector matched 8 of 10 records"},
	}
	b, err := json.Marshal(er)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j := string(b)
	for _, want := range []string{
		`"records":10`,
		`"fields":3`,
		`"sku"`,
		`"price: selector matched 8 of 10 records"`,
	} {
		if !strings.Contains(j, want) {
			t.Errorf("ExtractReport JSON missing %q; got %s", want, j)
		}
	}

	// Clean extraction (no missing, no warnings) → those keys omitted.
	er2 := ExtractReport{Records: 5, Fields: 2}
	b2, _ := json.Marshal(er2)
	if strings.Contains(string(b2), "missing") {
		t.Errorf("clean report should omit missing key; got %s", string(b2))
	}
	if strings.Contains(string(b2), "warnings") {
		t.Errorf("clean report should omit warnings key; got %s", string(b2))
	}
	if !strings.Contains(string(b2), `"records":5`) {
		t.Errorf("records missing from JSON; got %s", string(b2))
	}
}

// TestExtractActionValidation verifies the extract_structured entry in
// requiredFields: a missing/empty schema is rejected eagerly (#50 pattern),
// and a populated schema passes. This mirrors TestLoginActionValidation.
func TestExtractActionValidation(t *testing.T) {
	// Missing schema entirely → rejected.
	err := validateAction(Action{Type: "extract_structured"}, 0)
	if err == nil {
		t.Fatal("extract_structured with empty schema should fail validation")
	}
	if !IsActionValidationError(err) {
		t.Fatalf("expected ActionValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("error should mention schema; got %v", err)
	}

	// Populated schema → valid.
	err = validateAction(Action{
		Type: "extract_structured",
		ExtractSchema: map[string]FieldSpec{
			"name": {Selector: "h1"},
		},
	}, 0)
	if err != nil {
		t.Errorf("extract_structured with schema should pass validation; got %v", err)
	}
}

// TestParseActionsExtractSchema verifies ParseActions correctly parses the
// nested "schema" object and "container" string for an extract_structured
// action — the MCP client's JSON path that was the root cause of the login
// ParseActions bug fixed in this PR.
func TestParseActionsExtractSchema(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"type": "extract_structured",
			"schema": map[string]interface{}{
				"name":  map[string]interface{}{"selector": "h1"},
				"price": map[string]interface{}{"selector": ".price", "attr": "data-price"},
				"link":  map[string]interface{}{"selector": "a", "attr": "href"},
			},
			"container": ".product-card",
		},
	}
	actions, err := ParseActions(raw)
	if err != nil {
		t.Fatalf("ParseActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	a := actions[0]
	if a.Type != "extract_structured" {
		t.Errorf("type = %q, want extract_structured", a.Type)
	}
	if a.Container != ".product-card" {
		t.Errorf("container = %q, want .product-card", a.Container)
	}
	if len(a.ExtractSchema) != 3 {
		t.Fatalf("schema has %d fields, want 3", len(a.ExtractSchema))
	}
	// name: default attr resolves to "" (ExecuteExtract applies the "text"
	// default at probe-build time; the parsed FieldSpec keeps it empty).
	if a.ExtractSchema["name"].Selector != "h1" {
		t.Errorf("name.selector = %q, want h1", a.ExtractSchema["name"].Selector)
	}
	if a.ExtractSchema["price"].Attr != "data-price" {
		t.Errorf("price.attr = %q, want data-price", a.ExtractSchema["price"].Attr)
	}
	if a.ExtractSchema["link"].Attr != "href" {
		t.Errorf("link.attr = %q, want href", a.ExtractSchema["link"].Attr)
	}
}

// TestParseActionsExtractSchemaRejectsEmptySelector verifies a FieldSpec with
// an empty selector is rejected at parse time — it can never resolve and
// would only produce noise.
func TestParseActionsExtractSchemaRejectsEmptySelector(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"type": "extract_structured",
			"schema": map[string]interface{}{
				"bad": map[string]interface{}{"selector": ""},
			},
		},
	}
	_, err := ParseActions(raw)
	if err == nil {
		t.Fatal("extract_structured with empty selector should fail ParseActions")
	}
	if !strings.Contains(err.Error(), "empty selector") {
		t.Errorf("error should mention empty selector; got %v", err)
	}
}

// TestParseActionsLoginFieldsRoundTrip is the regression test for the login
// ParseActions bug fixed in this PR: login fields (username_selector,
// username, password_selector, password, submit_selector) must survive
// ParseActions — not just json.Unmarshal on the struct (which the pre-existing
// TestLoginActionFieldsRoundTrip covered, masking the bug). Before the fix,
// ParseActions only read type/selector/text/value/timeout/retries, so a login
// action from an MCP client had empty credentials and failed validation with
// "username_selector is required".
func TestParseActionsLoginFieldsRoundTrip(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"type":              "login",
			"username_selector": "#email",
			"username":          "user@example.com",
			"password_selector": "input[type=password]",
			"password":          "s3cret",
			"submit_selector":   "button[type=submit]",
		},
	}
	actions, err := ParseActions(raw)
	if err != nil {
		t.Fatalf("ParseActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	a := actions[0]
	if a.UsernameSelector != "#email" {
		t.Errorf("username_selector = %q, want #email", a.UsernameSelector)
	}
	if a.Username != "user@example.com" {
		t.Errorf("username = %q, want user@example.com", a.Username)
	}
	if a.PasswordSelector != "input[type=password]" {
		t.Errorf("password_selector = %q, want input[type=password]", a.PasswordSelector)
	}
	if a.Password != "s3cret" {
		t.Errorf("password = %q, want s3cret", a.Password)
	}
	if a.SubmitSelector != "button[type=submit]" {
		t.Errorf("submit_selector = %q, want button[type=submit]", a.SubmitSelector)
	}
}

// TestReplaceFirst is a small sanity check on the probe-template substitution
// helper used by ExecuteExtract.
func TestReplaceFirst(t *testing.T) {
	cases := []struct {
		s, old, repl, want string
	}{
		{"a__X__b__X__c", "__X__", "1", "a1b__X__c"},
		{"no match", "__X__", "1", "no match"},
		{"", "__X__", "1", ""},
		{"__X__", "__X__", `["a"]`, `["a"]`},
	}
	for _, c := range cases {
		if got := replaceFirst(c.s, c.old, c.repl); got != c.want {
			t.Errorf("replaceFirst(%q,%q,%q) = %q, want %q", c.s, c.old, c.repl, got, c.want)
		}
	}
}
