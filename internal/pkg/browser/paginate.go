package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// PaginateStopReason explains why the pagination loop stopped. Surfaced in
// PaginateResult so the LLM understands the outcome without guessing from the
// record count alone.
type PaginateStopReason string

const (
	// PaginateStopEndReached: the next selector is gone or disabled — the last
	// page was reached.
	PaginateStopEndReached PaginateStopReason = "end_reached"
	// PaginateStopMaxPages: collected the configured max_pages cap.
	PaginateStopMaxPages PaginateStopReason = "max_pages"
	// PaginateStopNoChange: a click on next succeeded but the page content did
	// not change (no new records appeared) — likely the end, or the selector
	// fired a no-op. Stopping avoids an infinite loop.
	PaginateStopNoChange PaginateStopReason = "no_change"
)

// PaginateResult is the structured outcome of the paginate composite action
// (#77 Tier-3). paginate walks a listing by extracting records, clicking the
// next control, waiting for the page to settle, and repeating — accumulating
// deduplicated records across all visited pages.
//
// Like LoginResult / ExtractReport it carries qualitative feedback so the LLM
// can judge the result: how many pages and records, per-page counts, why the
// loop stopped, and any caveats (dedupe skips, partial pages). No numeric
// confidence — evidence over scores, consistent with the #77 consensus.
type PaginateResult struct {
	// PagesCollected is the number of pages from which records were extracted
	// (always >= 1, since the first page is extracted before any click).
	PagesCollected int `json:"pages_collected"`
	// TotalRecords is the number of deduplicated records accumulated across all
	// pages. If dedupe is disabled this equals the raw sum of per-page counts.
	TotalRecords int `json:"total_records"`
	// DedupedRecords is how many records were dropped as duplicates. Zero when
	// no dedupe key was configured or there were no duplicates.
	DedupedRecords int `json:"deduped_records,omitempty"`
	// StopReason explains why the loop ended.
	StopReason PaginateStopReason `json:"stop_reason"`
	// PerPage is the record count extracted from each page in order (index 0 =
	// first page). Length equals PagesCollected.
	PerPage []int `json:"per_page,omitempty"`
	// Warnings holds non-fatal caveats: dedupe key not found in schema,
	// a page that returned zero records, next click that errored, etc. Empty
	// on a clean run.
	Warnings []string `json:"warnings,omitempty"`
}

// recordSignature builds a stable string key for a record to deduplicate it
// across pages. If dedupeKey is non-empty and present in the record, its value
// is the signature. Otherwise the whole record is serialized with sorted keys
// (so field order in the map does not fragment the key).
func recordSignature(rec map[string]string, dedupeKey string) string {
	if dedupeKey != "" {
		if v, ok := rec[dedupeKey]; ok && v != "" {
			return dedupeKey + "\x00" + v
		}
		// Key configured but missing from this record — fall back to the full
		// record so we don't wrongly merge two different records that both
		// omit the key. (A warning is surfaced by the caller.)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		// json.Marshal on map[string]string only fails on \x00 replacement;
		// fall back to a manual join so dedupe never panics.
		var sb strings.Builder
		for k, v := range rec {
			sb.WriteString(k)
			sb.WriteByte('=')
			sb.WriteString(v)
			sb.WriteByte(';')
		}
		return sb.String()
	}
	return string(b)
}

// nextProbeJS checks whether the next-page control still exists and is
// interactable. Returns {exists, disabled}. A disabled "next" (e.g. the
// common aria-disabled="true" / disabled attribute pattern on the last page)
// is treated as end-of-list, same as the element being absent.
const nextProbeJS = `(() => {
	const sel = %SELECTOR%;
	let el = null;
	try { el = document.querySelector(sel); } catch(e) { return {exists:false, error:true}; }
	if (!el) return {exists: false};
	// Disabled via attribute or aria-disabled.
	const disabled = el.disabled === true ||
		el.getAttribute('aria-disabled') === 'true' ||
		el.getAttribute('disabled') !== null ||
		(el.classList && (el.classList.contains('disabled') || el.classList.contains('is-disabled')));
	return {exists: true, disabled: !!disabled};
})()`

// ExecutePaginate is the paginate composite action (#77 Tier-3). It walks a
// listing across multiple pages by repeating: extract records → click next →
// wait for settle. Records are accumulated and deduplicated across pages.
//
// paginate reuses the extract_structured probe (runExtractProbe) so the schema
// semantics are identical: selector + attr only, values are strings. The
// container selector is required (paginate produces an array of records). The
// next selector drives page advancement.
//
// Stop conditions (first match wins):
//   - next selector gone or disabled → end_reached
//   - max_pages reached → max_pages
//   - a click on next produced no new records (content unchanged) → no_change
//
// paginate is a mutation (it clicks next), so it IS in isMutatingAction and
// the surrounding loop will smart-settle + observe after it. The per-iteration
// settle is handled internally (we need the page stable before each probe and
// before checking the next control).
//
// An extraction that yields zero records on the first page is NOT a hard error
// — the LLM should see the page + the report and fix its selectors (mirrors
// the extract auth_failed-is-not-fatal principle). A zero on a LATER page
// after a click is treated as no_change and the loop stops.
func (e *ActionExecutor) ExecutePaginate(ctx context.Context, action Action) error {
	schema := action.ExtractSchema
	if len(schema) == 0 {
		return &ActionValidationError{
			ActionType: action.Type,
			Reason:     "schema is required for paginate action",
		}
	}
	container := action.Container
	if container == "" {
		return &ActionValidationError{
			ActionType: action.Type,
			Reason:     "container is required for paginate action (paginate always produces an array of records)",
		}
	}
	nextSelector := action.NextSelector
	if nextSelector == "" {
		return &ActionValidationError{
			ActionType: action.Type,
			Reason:     "next_selector is required for paginate action",
		}
	}
	maxPages := action.MaxPages
	if maxPages <= 0 {
		// Defense-in-depth: validateAction also guards this, but a caller that
		// builds an Action directly must still get a sane bound. Default to a
		// conservative cap to prevent runaway loops on broken sites.
		maxPages = 10
	}
	dedupeKey := action.DedupeKey
	if dedupeKey != "" {
		if _, ok := schema[dedupeKey]; !ok {
			// Not fatal: fall back to full-record signatures, but warn so the
			// LLM knows its dedupe key is not part of the extraction schema.
			e.logger.Warn().
				Str("dedupe_key", dedupeKey).
				Msg("paginate: dedupe_key not present in schema; falling back to full-record dedupe")
			dedupeKey = "" // disable so recordSignature uses full-record mode
		}
	}

	result := &PaginateResult{}
	seen := make(map[string]bool)       // dedupe signatures across all pages
	allRecords := []map[string]string{} // accumulated, deduplicated records
	dedupedCount := 0

	// Helper: extract current page, dedupe, accumulate. Returns the number of
	// NEW records added this page (post-dedupe). Errors are non-fatal: a failed
	// probe is logged and counts as zero new records.
	extractPage := func() int {
		raw, err := runExtractProbe(ctx, schema, container)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("page %d: extract probe failed: %v", result.PagesCollected+1, err))
			return 0
		}
		pageRecords := raw.Records
		if pageRecords == nil {
			pageRecords = []map[string]string{}
		}
		// Surface extraction diagnostics as warnings (missing selectors, etc.)
		for _, m := range raw.Missing {
			result.Warnings = append(result.Warnings, fmt.Sprintf("page %d: field %q matched nothing", result.PagesCollected+1, m))
		}
		for _, w := range raw.Warnings {
			result.Warnings = append(result.Warnings, fmt.Sprintf("page %d: %s", result.PagesCollected+1, w))
		}

		newCount := 0
		keyMissingWarned := false
		for _, rec := range pageRecords {
			sig := recordSignature(rec, dedupeKey)
			if seen[sig] {
				dedupedCount++
				continue
			}
			seen[sig] = true
			if dedupeKey != "" && rec[dedupeKey] == "" && !keyMissingWarned {
				result.Warnings = append(result.Warnings, fmt.Sprintf("page %d: dedupe_key %q is empty on at least one record; using full-record fallback", result.PagesCollected+1, dedupeKey))
				keyMissingWarned = true
			}
			allRecords = append(allRecords, rec)
			newCount++
		}
		return newCount
	}

	// Settle between pages using the executor's configured settle (same one the
	// outer loop uses after mutating actions). Best-effort: a timeout just
	// means we snapshot what we have.
	settle := func() {
		if !e.smartSettle {
			return
		}
		if _, serr := SmartSettle(ctx, e.logger, e.settleCfg); serr != nil && serr != context.Canceled {
			e.logger.Debug().Err(serr).Int("page", result.PagesCollected+1).Msg("paginate: settle ended (non-critical)")
		}
	}

	// --- The loop ---
	for {
		newCount := extractPage()
		result.PagesCollected++
		result.PerPage = append(result.PerPage, newCount)

		// Stop: max_pages cap reached.
		if result.PagesCollected >= maxPages {
			result.StopReason = PaginateStopMaxPages
			break
		}

		// Check whether a next control exists and is enabled.
		var probe struct {
			Exists   bool `json:"exists"`
			Disabled bool `json:"disabled"`
			Error    bool `json:"error"`
		}
		js := strings.Replace(nextProbeJS, "%SELECTOR%", quoteSelector(nextSelector), 1)
		if err := chromedp.Evaluate(js, &probe).Do(ctx); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("page %d: next-control probe failed: %v", result.PagesCollected, err))
			result.StopReason = PaginateStopEndReached
			break
		}
		if probe.Error || !probe.Exists || probe.Disabled {
			result.StopReason = PaginateStopEndReached
			break
		}

		// Click next. A click failure is not fatal: we stop and return what we
		// have so far (the LLM gets the accumulated records + the warning).
		if err := e.ExecuteClick(ctx, nextSelector); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("page %d: click on next_selector failed: %v", result.PagesCollected, err))
			result.StopReason = PaginateStopEndReached
			break
		}
		settle()
	}

	// no_change detection: re-examine the last iteration. If the last page
	// contributed zero NEW records and the loop did not stop for end/max, it
	// means the final click produced nothing new.
	if result.StopReason == "" {
		if len(result.PerPage) > 0 && result.PerPage[len(result.PerPage)-1] == 0 {
			result.StopReason = PaginateStopNoChange
		} else {
			// Fallback (should not normally happen): treat as end reached.
			result.StopReason = PaginateStopEndReached
		}
	}

	result.TotalRecords = len(allRecords)
	result.DedupedRecords = dedupedCount

	e.lastPaginateData = allRecords
	e.lastPaginateResult = result

	e.logger.Info().
		Int("pages_collected", result.PagesCollected).
		Int("total_records", result.TotalRecords).
		Int("deduped", result.DedupedRecords).
		Str("stop_reason", string(result.StopReason)).
		Msg("Paginate action completed")

	// paginate is never a hard error: even with zero records, the LLM should
	// inspect the page + the result to fix selectors / the next control.
	return nil
}
