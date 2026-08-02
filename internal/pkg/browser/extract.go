package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// ExtractReport is the structured outcome of an extract_structured action
// (#77 Tier-3), surfaced alongside the data in metadata. Like LoginResult it
// carries qualitative feedback so the LLM can judge whether the extraction
// succeeded: which fields came back empty, which selectors matched nothing,
// how many records were found. No numeric confidence — evidence over scores,
// consistent with the #77 consensus.
type ExtractReport struct {
	// Records is the number of objects in extracted_data: 1 for a single
	// (container-less) extract, N when a container selector matched N rows.
	Records int `json:"records"`
	// Fields is the total number of fields in the schema.
	Fields int `json:"fields"`
	// Missing lists output field names whose value is empty across ALL
	// records (the selector matched nothing anywhere). These are the fields
	// whose selectors most likely need fixing.
	Missing []string `json:"missing,omitempty"`
	// Warnings holds per-record/per-field caveats, e.g.
	// "price: selector '.price' matched 0 of 10 cards". Empty on a clean
	// extraction where every field resolved in every record.
	Warnings []string `json:"warnings,omitempty"`
}

// extractResult is the raw JSON returned by the DOM probe.
type extractResult struct {
	Records  []map[string]string `json:"records"`
	Missing  []string            `json:"missing"`
	Warnings []string            `json:"warnings"`
}

// extractProbeJS is the DOM probe. It runs as a single Evaluate round-trip.
// fieldsJSON is a JSON array of {name,selector,attr} descriptors injected by
// ExecuteExtract; containerJSON is a JSON string literal ("" for single mode).
//
// Walk every container element (or [document] when none) and resolve each
// field's selector + attribute. Track per-field hit counts so a total miss is
// reported once (in missing) and a partial miss separately (in warnings).
const extractProbeJS = `(() => {
	const fields = __FIELDS__;
	const container = __CONTAINER__;
	const missing = [];
	const warnings = [];
	const hits = Object.create(null);
	fields.forEach(f => { hits[f.name] = 0; });

	const containers = container ? Array.from(document.querySelectorAll(container)) : [document];
	if (containers.length === 0) {
		warnings.push("container selector matched 0 elements");
	}

	const records = containers.map(root => {
		const rec = {};
		for (const f of fields) {
			const el = root.querySelector(f.selector);
			let val = "";
			if (el) {
				hits[f.name]++;
				val = f.attr === "text" ? (el.innerText || "").trim()
				                      : (el.getAttribute(f.attr) || "").trim();
			}
			rec[f.name] = val;
		}
		return rec;
	});

	// Missing = fields with zero hits across all records.
	fields.forEach(f => {
		if (hits[f.name] === 0) missing.push(f.name);
	});

	// Partial-match warning: a total miss is already in the missing list;
	// only flag fields that resolved in some records but not all.
	fields.forEach(f => {
		if (hits[f.name] > 0 && hits[f.name] < containers.length) {
			warnings.push(f.name + ": selector matched " + hits[f.name] + " of " + containers.length + " records");
		}
	});

	return { records, missing, warnings };
})()`

// ExecuteExtract is the extract_structured composite action (#77 Tier-3). It
// runs a single chromedp.Evaluate that walks every container element (or the
// document once when no container is set) and, for each FieldSpec, resolves
// the selector + attribute. The whole schema x container pass happens in one
// JS round-trip to keep overhead minimal.
//
// Design (variant 2, #77): selector + attr only, no type coercion. Values are
// always strings. This keeps the tool transparent — what's in the DOM is what
// comes back — and avoids silent truncation of locale-formatted numbers. The
// LLM parses values in downstream logic; the tool's job is reliable DOM
// selection, not interpretation.
//
// extract is read-only: it does NOT mutate page state and is NOT in
// isMutatingAction, so it triggers no settle/observation snapshot. An empty
// extraction (all fields blank) is NOT a hard error — the page loaded and the
// LLM should see it plus the report so it can fix its selectors (mirrors the
// login auth_failed-is-not-fatal principle).
// fieldDesc is a stable, ordered descriptor for one schema field, used by the
// JS probe. Shared by ExecuteExtract and ExecutePaginate via runExtractProbe.
type fieldDesc struct {
	Name     string `json:"name"`
	Selector string `json:"selector"`
	Attr     string `json:"attr"`
}

// buildFieldDescs turns a schema map into a stable ordered list of descriptors,
// applying the default attr ("text") centrally.
func buildFieldDescs(schema map[string]FieldSpec) []fieldDesc {
	descs := make([]fieldDesc, 0, len(schema))
	for name, spec := range schema {
		attr := spec.Attr
		if attr == "" {
			attr = "text"
		}
		descs = append(descs, fieldDesc{Name: name, Selector: spec.Selector, Attr: attr})
	}
	return descs
}

// runExtractProbe is the shared DOM-extraction core used by both
// extract_structured and paginate. It builds the field descriptors, injects
// them plus the container selector into the probe template, and runs a single
// chromedp.Evaluate. Returns the raw probe result (records/missing/warnings).
// The caller normalizes the shape and builds the ExtractReport.
func runExtractProbe(ctx context.Context, schema map[string]FieldSpec, container string) (extractResult, error) {
	descs := buildFieldDescs(schema)
	fieldsJSON, err := json.Marshal(descs)
	if err != nil {
		return extractResult{}, fmt.Errorf("extract: marshal field descriptors: %w", err)
	}
	containerJSON, err := json.Marshal(container)
	if err != nil {
		return extractResult{}, fmt.Errorf("extract: marshal container: %w", err)
	}

	// Inject the JSON values into the probe template. Both are valid JSON
	// literals, so this is safe (no quoting/escaping pitfalls).
	js := extractProbeJS
	js = replaceFirst(js, "__FIELDS__", string(fieldsJSON))
	js = replaceFirst(js, "__CONTAINER__", string(containerJSON))

	var raw extractResult
	if err := chromedp.Evaluate(js, &raw).Do(ctx); err != nil {
		return extractResult{}, fmt.Errorf("extract: DOM probe failed: %w", err)
	}
	return raw, nil
}

// extractReport builds the qualitative ExtractReport from a raw probe result.
func extractReport(raw extractResult, schemaFields int) ExtractReport {
	return ExtractReport{
		Records:  len(raw.Records),
		Fields:   schemaFields,
		Missing:  raw.Missing,
		Warnings: raw.Warnings,
	}
}

// normalizeExtractData shapes the raw probe result for single vs container
// mode: container-less → one object; container set → array of objects.
func normalizeExtractData(raw extractResult, container string) interface{} {
	if container == "" {
		if len(raw.Records) > 0 {
			return raw.Records[0]
		}
		return map[string]string{}
	}
	if raw.Records == nil {
		return []map[string]string{}
	}
	return raw.Records
}

func (e *ActionExecutor) ExecuteExtract(ctx context.Context, action Action) error {
	schema := action.ExtractSchema
	if len(schema) == 0 {
		// validateAction already guards this, but keep defense-in-depth.
		return &ActionValidationError{
			ActionType: action.Type,
			Reason:     "schema is required for extract_structured action",
		}
	}

	raw, err := runExtractProbe(ctx, schema, action.Container)
	if err != nil {
		return err
	}

	report := extractReport(raw, len(schema))

	// Normalize the data shape: container-less extract returns one record.
	data := normalizeExtractData(raw, action.Container)

	e.lastExtractData = data
	e.lastExtractReport = &report

	e.logger.Info().
		Int("records", report.Records).
		Int("fields", report.Fields).
		Strs("missing", report.Missing).
		Strs("warnings", report.Warnings).
		Msg("Extract structured action completed")

	// extract is NOT a hard error even when every field is empty — the page
	// loaded and the LLM should inspect it + the report to fix selectors.
	return nil
}

// replaceFirst replaces the first occurrence of old in s with replacement.
// A small helper to avoid pulling in strings.ReplaceN semantics; used only for
// the two probe-template substitutions.
func replaceFirst(s, old, replacement string) string {
	idx := strings.Index(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + replacement + s[idx+len(old):]
}

// parseExtractSchema converts the raw "schema" value from a parsed MCP action
// (an untyped map[string]interface{}) into a typed map[string]FieldSpec.
// Marshal→typed-unmarshal validates field names/shapes and lets FieldSpec Attr
// defaults apply in one place. Returns an error suitable for wrapping in
// ParseActions (reports the action index).
func parseExtractSchema(raw interface{}) (map[string]FieldSpec, error) {
	// JSON-decoded objects arrive as map[string]interface{}.
	// When the MCP transport already gave a typed map we accept it too.
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("schema is not a JSON-serializable object: %w", err)
	}
	schema := map[string]FieldSpec{}
	if err := json.Unmarshal(b, &schema); err != nil {
		return nil, fmt.Errorf("schema is not an object of {selector,attr} specs: %w", err)
	}
	// Reject specs with an empty selector — they can never resolve.
	for name, spec := range schema {
		if strings.TrimSpace(spec.Selector) == "" {
			return nil, fmt.Errorf("schema field %q has an empty selector", name)
		}
	}
	return schema, nil
}
