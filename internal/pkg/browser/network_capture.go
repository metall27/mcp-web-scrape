package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/network"
)

// CapturedResponse holds the body of an API/XHR response that was intercepted
// because it looked like it might carry the page's real content (#83). The body
// is truncated to MaxBodyBytes to keep metadata bounded.
type CapturedResponse struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type,omitempty"`
	// Body is the response body (possibly truncated). For base64-encoded
	// responses (binary), the decoded UTF-8 text is stored; if decoding fails
	// the raw base64 string is kept.
	Body string `json:"body"`
	// Size is the original response body size in bytes (before truncation).
	Size int64 `json:"size"`
	// Truncated is true when the body exceeded MaxBodyBytes and was cut.
	Truncated bool `json:"truncated,omitempty"`
}

// CaptureConfig controls how much data CaptureResponseBodies pulls from Chrome.
// Defaults are conservative to avoid flooding LLM context with large API
// payloads (e.g. rebrainme's /api/v2/tasks/3706 returns 87KB JSON).
type CaptureConfig struct {
	// MaxBodyBytes caps a single response body. Responses larger than this are
	// truncated. Default: 51200 (50KB) — enough for lesson text, cuts bulk.
	MaxBodyBytes int

	// MaxTotalBytes caps the sum of all captured bodies across a single scrape.
	// Once the budget is exhausted, remaining candidates are skipped. Default:
	// 102400 (100KB) — room for 2-3 API responses, not a data dump.
	MaxTotalBytes int

	// MaxResponses caps the number of responses captured, regardless of size.
	// Prevents pathological pages with dozens of API calls from flooding
	// metadata. Default: 5.
	MaxResponses int
}

// DefaultCaptureConfig returns conservative limits: 50KB per body, 100KB total,
// at most 5 responses.
func DefaultCaptureConfig() CaptureConfig {
	return CaptureConfig{
		MaxBodyBytes:  51200,
		MaxTotalBytes: 102400,
		MaxResponses:  5,
	}
}

// CaptureResponseBodies fetches the response bodies for the given CDP request
// IDs via network.GetResponseBody. This is the capture half of #83: the
// detection (ContentCandidates) decided these responses likely carry content
// the DOM doesn't reflect; this function pulls the actual bytes.
//
// Must be called inside a chromedp.ActionFunc with the same CDP context that
// observed the requests (the request IDs are session-scoped). Non-fatal: a
// failed GetResponseBody for one request does not abort the rest — it's simply
// skipped, and a Debug log is emitted.
//
// Bodies are truncated per CaptureConfig and the total is capped. Responses are
// returned in the order their IDs were supplied.
func CaptureResponseBodies(ctx context.Context, monitor *NetworkMonitor, reqIDs []string, cfg CaptureConfig) []CapturedResponse {
	if monitor == nil || len(reqIDs) == 0 {
		return nil
	}

	var results []CapturedResponse
	totalBytes := 0

	for _, idStr := range reqIDs {
		if len(results) >= cfg.MaxResponses {
			break
		}
		if totalBytes >= cfg.MaxTotalBytes {
			break
		}

		// network.GetResponseBody requires the page to have finished loading
		// the resource. Since we only call this after the scrape completes
		// (settle + actions done), all tracked requests have fired
		// LoadingFinished — the body is available. The Do() method already
		// decodes base64-encoded bodies and returns raw []byte.
		rawBody, err := network.GetResponseBody(network.RequestID(idStr)).Do(ctx)
		if err != nil {
			// Non-fatal: the response may have been evicted from Chrome's
			// buffer, or the request belongs to a detached context. Skip it.
			continue
		}

		body := string(rawBody)

		origSize := int64(len(body))
		truncated := false

		// Per-body cap.
		remaining := cfg.MaxTotalBytes - totalBytes
		bodyLimit := cfg.MaxBodyBytes
		if remaining < bodyLimit {
			bodyLimit = remaining
		}
		if len(body) > bodyLimit {
			body = body[:bodyLimit]
			truncated = true
		}

		// Look up URL + MIME from the monitor's request log.
		var url, mimeType string
		if nr := monitor.findRequestByID(idStr); nr != nil {
			url = nr.URL
			mimeType = nr.MimeType
		}

		results = append(results, CapturedResponse{
			URL:       url,
			MimeType:  mimeType,
			Body:      body,
			Size:      origSize,
			Truncated: truncated,
		})
		totalBytes += len(body)
	}

	return results
}

// findRequestByID looks up a recorded request by its CDP request ID. Returns
// nil if not found (e.g. the request exceeded maxRecordedRequests or belongs to
// a different monitor instance).
func (m *NetworkMonitor) findRequestByID(idStr string) *NetworkRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.requests {
		if m.requests[i].RequestID == idStr {
			return &m.requests[i]
		}
	}
	return nil
}

// ContentCandidatesFromCaptured converts captured response bodies to
// ContentCandidate entries (URL + MIME + size) for diagnostics metadata. Used
// when the caller wants to report WHAT was captured without including the
// bodies themselves.
func ContentCandidatesFromCaptured(captured []CapturedResponse) []ContentCandidate {
	if len(captured) == 0 {
		return nil
	}
	out := make([]ContentCandidate, len(captured))
	for i, c := range captured {
		out[i] = ContentCandidate{
			URL:               c.URL,
			MimeType:          c.MimeType,
			EncodedDataLength: c.Size,
		}
	}
	return out
}

// formatCaptureSummary returns a compact one-line description of captured
// responses for logging, e.g. "2 responses, 42.5KB total".
func formatCaptureSummary(captured []CapturedResponse) string {
	if len(captured) == 0 {
		return "0 responses"
	}
	total := int64(0)
	for _, c := range captured {
		total += c.Size
	}
	return fmt.Sprintf("%d responses, %.1fKB total", len(captured), float64(total)/1024)
}
