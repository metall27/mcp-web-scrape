package tools

import (
	"context"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// minAPIContentSize is the minimum total size of content-candidate responses
// for the detection heuristic to fire. Below this we assume the API responses
// are config/tracking noise, not page content.
//
// rebrainme (#83): /api/v2/tasks/3706 returns 87KB JSON. A typical SPA config
// call is 1-5KB. 10KB separates "the API carries real content" from "the API
// carries a small config blob".
const minAPIContentSize int64 = 10240 // 10KB

// detectContentCandidates applies the #83 detection heuristic: is this a SPA
// where the page's real content lives in API responses rather than the rendered
// DOM?
//
// The heuristic:
//  1. The page must be a SPA (classify_page said IsSPA=true).
//  2. There must be XHR/Fetch responses with JSON/text MIME, status 200, and
//     size ≥ 2KB (content candidates from NetworkMonitor).
//  3. The total size of content candidates must be ≥ minAPIContentSize (10KB).
//     This excludes SPAs that make small config/tracking calls alongside a
//     full DOM render.
//
// We do NOT compare API size to HTML size: a SPA shell (nav, sidebar, scripts)
// can be 80KB with near-zero meaningful content, making a size ratio
// unreliable. The absolute API size is a cleaner signal — a 10KB+ JSON/text
// XHR on a SPA almost certainly carries page data.
//
// When the heuristic fires, the returned candidates are non-empty. The caller
// then captures the actual response bodies via CaptureResponseBodies.
//
// ctx is currently unused (detection is pure computation on already-collected
// data), but accepted for future probes that may need a CDP round-trip.
func detectContentCandidates(_ context.Context, monitor *browser.NetworkMonitor, signals *browser.DOMSignals) []browser.ContentCandidate {
	if monitor == nil || signals == nil || !signals.IsSPA {
		return nil
	}

	filter := browser.DefaultContentCandidateFilter()
	candidates := monitor.ContentCandidates(filter)
	if len(candidates) == 0 {
		return nil
	}

	// Sum the total size of content candidates.
	var apiTotal int64
	for _, c := range candidates {
		apiTotal += c.EncodedDataLength
	}

	// Absolute threshold: 10KB+ of JSON/text API responses on a SPA is a
	// strong signal that content lives in the API.
	if apiTotal < minAPIContentSize {
		return nil
	}

	return candidates
}
