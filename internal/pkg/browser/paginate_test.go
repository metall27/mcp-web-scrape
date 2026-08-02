package browser

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPaginateResultJSON verifies the metadata-facing JSON shape of
// PaginateResult — the contract js_tool.go surfaces in metadata.paginate_result.
func TestPaginateResultJSON(t *testing.T) {
	// Full result with dedupe + warnings.
	pr := PaginateResult{
		PagesCollected: 3,
		TotalRecords:   28,
		DedupedRecords: 2,
		StopReason:     PaginateStopEndReached,
		PerPage:        []int{10, 10, 8},
		Warnings:       []string{"page 2: field \"sku\" matched nothing"},
	}
	b, err := json.Marshal(pr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j := string(b)
	for _, want := range []string{
		`"pages_collected":3`,
		`"total_records":28`,
		`"deduped_records":2`,
		`"stop_reason":"end_reached"`,
		`"per_page":[10,10,8]`,
		`page 2: field`, // warning content preserved
	} {
		if !strings.Contains(j, want) {
			t.Errorf("PaginateResult JSON missing %q; got %s", want, j)
		}
	}

	// Clean run (no dedupe, no warnings) → those keys omitted.
	pr2 := PaginateResult{
		PagesCollected: 2,
		TotalRecords:   20,
		StopReason:     PaginateStopMaxPages,
		PerPage:        []int{10, 10},
	}
	b2, _ := json.Marshal(pr2)
	if strings.Contains(string(b2), "deduped_records") {
		t.Errorf("clean result should omit deduped_records; got %s", string(b2))
	}
	if strings.Contains(string(b2), "warnings") {
		t.Errorf("clean result should omit warnings; got %s", string(b2))
	}
	if !strings.Contains(string(b2), `"stop_reason":"max_pages"`) {
		t.Errorf("stop_reason missing; got %s", string(b2))
	}
}

// TestPaginateActionValidation verifies the paginate entry in requiredFields:
// missing next_selector / schema / container / max_pages are all rejected
// eagerly (#50 pattern), and a fully-specified action passes.
func TestPaginateActionValidation(t *testing.T) {
	schema := map[string]FieldSpec{"name": {Selector: "h1"}}

	cases := []struct {
		name   string
		action Action
		want   string // substring expected in the error, "" = should pass
	}{
		{"missing next_selector", Action{
			Type: "paginate", ExtractSchema: schema, Container: ".card", MaxPages: 5,
		}, "next_selector"},
		{"missing schema", Action{
			Type: "paginate", NextSelector: ".next", Container: ".card", MaxPages: 5,
		}, "schema"},
		{"missing container", Action{
			Type: "paginate", NextSelector: ".next", ExtractSchema: schema, MaxPages: 5,
		}, "container"},
		{"max_pages zero", Action{
			Type: "paginate", NextSelector: ".next", ExtractSchema: schema, Container: ".card", MaxPages: 0,
		}, "max_pages"},
		{"valid full", Action{
			Type: "paginate", NextSelector: ".next", ExtractSchema: schema, Container: ".card", MaxPages: 5,
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAction(c.action, 0)
			if c.want == "" {
				if err != nil {
					t.Errorf("expected pass, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error mentioning %q, got nil", c.want)
			}
			if !IsActionValidationError(err) {
				t.Fatalf("expected ActionValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error should mention %q; got %v", c.want, err)
			}
		})
	}
}

// TestParseActionsPaginateFields verifies ParseActions correctly parses the
// paginate-specific fields (next_selector, max_pages, dedupe_key) from the
// MCP client's JSON — the same path that the login ParseActions bug (#81)
// broke. schema/container reuse the extract parsing.
func TestParseActionsPaginateFields(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"type":          "paginate",
			"next_selector": "a.next-page",
			"max_pages":     float64(5),
			"dedupe_key":    "sku",
			"container":     ".product-card",
			"schema": map[string]interface{}{
				"name": map[string]interface{}{"selector": "h3"},
				"sku":  map[string]interface{}{"selector": "[data-sku]", "attr": "data-sku"},
			},
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
	if a.Type != "paginate" {
		t.Errorf("type = %q, want paginate", a.Type)
	}
	if a.NextSelector != "a.next-page" {
		t.Errorf("next_selector = %q, want a.next-page", a.NextSelector)
	}
	if a.MaxPages != 5 {
		t.Errorf("max_pages = %d, want 5", a.MaxPages)
	}
	if a.DedupeKey != "sku" {
		t.Errorf("dedupe_key = %q, want sku", a.DedupeKey)
	}
	if a.Container != ".product-card" {
		t.Errorf("container = %q, want .product-card", a.Container)
	}
	if len(a.ExtractSchema) != 2 {
		t.Fatalf("schema has %d fields, want 2", len(a.ExtractSchema))
	}
	if a.ExtractSchema["sku"].Attr != "data-sku" {
		t.Errorf("sku.attr = %q, want data-sku", a.ExtractSchema["sku"].Attr)
	}
}

// TestRecordSignature exercises the cross-page deduplication key builder.
// When dedupeKey is set and present, the key is keyed-field + value; otherwise
// the full record (with stable key order via json.Marshal) is used.
func TestRecordSignature(t *testing.T) {
	rec := map[string]string{"name": "Widget", "sku": "ABC", "price": "9.99"}

	// dedupeKey present → stable key regardless of map iteration order.
	sig1 := recordSignature(rec, "sku")
	sig2 := recordSignature(rec, "sku")
	if sig1 != sig2 {
		t.Errorf("same record produced different signatures: %q vs %q", sig1, sig2)
	}
	if !strings.Contains(sig1, "ABC") {
		t.Errorf("dedupe signature should contain the sku value; got %q", sig1)
	}

	// Two records with the same sku collide; two with different skus don't.
	rec2 := map[string]string{"name": "Other", "sku": "ABC", "price": "1.00"}
	if recordSignature(rec, "sku") != recordSignature(rec2, "sku") {
		t.Errorf("records with same sku should collide")
	}
	rec3 := map[string]string{"name": "Widget", "sku": "XYZ", "price": "9.99"}
	if recordSignature(rec, "sku") == recordSignature(rec3, "sku") {
		t.Errorf("records with different sku should not collide")
	}

	// No dedupeKey → full-record signature: different content → different sig.
	fullA := recordSignature(map[string]string{"a": "1", "b": "2"}, "")
	fullB := recordSignature(map[string]string{"a": "1", "b": "2"}, "")
	if fullA != fullB {
		t.Errorf("identical records should produce identical full-record signatures")
	}
	fullC := recordSignature(map[string]string{"a": "1", "b": "3"}, "")
	if fullA == fullC {
		t.Errorf("different records should produce different full-record signatures")
	}

	// dedupeKey configured but missing from record → fall back to full record
	// (so two different records that both omit the key don't wrongly merge).
	missA := recordSignature(map[string]string{"x": "1"}, "sku")
	missB := recordSignature(map[string]string{"x": "2"}, "sku")
	if missA == missB {
		t.Errorf("records missing the dedupe key but differing otherwise should not collide")
	}
}
