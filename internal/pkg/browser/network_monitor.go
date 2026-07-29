package browser

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// NetworkMonitor captures CDP Network domain events during a scrape attempt.
// It replaces the Performance-API polling used by NetworkIdleAdvanced with real
// CDP events: actual HTTP status codes (401/403 detected instantly, before any
// settle window), an inflight-request counter (true network-idle without
// polling), and a summary of auth-relevant requests.
//
// This is the foundation for the "smart tool" vision (#77): rich diagnostics
// need real network data, not guesses. The monitor is read-only — it observes
// and records, it never mutates page state.
//
// Lifecycle: one monitor per scrape attempt. Start() registers a CDP listener
// on the given chromedp context and enables the Network domain. The listener
// goroutine is bound to that context and exits when the context is canceled
// (scrape ends / browser context released). Stop() is a no-op safety net — the
// context cancellation does the real cleanup.
//
// Thread-safety: all fields are protected by mu, EXCEPT inflight which uses
// atomic ops (it's the hottest path — incremented on every request, decremented
// on every finish, polled by idle checks).
type NetworkMonitor struct {
	mu sync.Mutex

	// inflight counts requests started but not yet finished. Atomic for the hot
	// path. Decrement happens on EventLoadingFinished AND EventLoadingFailed
	// (failed requests never reach Finished — without the Failed handler the
	// counter leaks and IsIdle() sticks false forever).
	inflight int64

	// lastActivityAt tracks when the most recent network response arrived, for
	// idle detection (inflight==0 for N ms).
	lastActivityAt atomic.Int64 // unix milli

	// completed counts requests that finished (for summary stats).
	completed int64

	// authFailures records requests that returned 401/403 — the key signal for
	// auth-required pages (#71 diagnosis showed /api/v2/tasks → 401 before login).
	authFailures []AuthFailure

	// blockedCDN true if a response hints at CDN/bot blocking (403 + known CDN
	// headers, 429, or 503 with retry-after). Heuristic — flagged for the LLM,
	// not a verdict.
	blockedCDN bool

	// requests records method + url + status for up to maxRecordedRequests, for
	// the NetworkSummary returned in diagnostics. Bounded to avoid unbounded
	// memory on request-heavy pages.
	requests []NetworkRequest

	// started guards against double-registration of the CDP listener.
	started bool
}

// AuthFailure records a single 401/403 response — the signal that a page
// requires authentication or the current session is invalid.
type AuthFailure struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
}

// NetworkRequest is a single observed request/response, for diagnostics.
type NetworkRequest struct {
	Method            string `json:"method,omitempty"` // GET, POST, ... (from request)
	URL               string `json:"url"`
	Status            int    `json:"status"`
	RequestID         string `json:"-"`                             // CDP request id — internal, not serialized
	MimeType          string `json:"mime_type,omitempty"`           // application/json, text/html, ...
	ResourceType      string `json:"resource_type,omitempty"`       // XHR, Fetch, Document, ...
	EncodedDataLength int64  `json:"encoded_data_length,omitempty"` // final body size in bytes (updated on LoadingFinished)
}

// maxRecordedRequests caps the request log so a request-heavy SPA doesn't
// blow up metadata. 64 is enough to spot auth/api patterns without flooding
// context.
const maxRecordedRequests = 64

// NewNetworkMonitor returns a fresh monitor ready to Start.
func NewNetworkMonitor() *NetworkMonitor {
	return &NetworkMonitor{}
}

// Start enables the CDP Network domain and registers event listeners on the
// given chromedp context. Must be called inside a chromedp.ActionFunc (the ctx
// is the CDP session context). Safe to call once per monitor; a second call is
// a no-op. Returns an error if network.Enable fails (non-fatal callers may
// ignore it and fall back to the polling-based NetworkIdleAdvanced).
func (m *NetworkMonitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	// Enable the Network domain so CDP emits request/response events.
	if err := network.Enable().Do(ctx); err != nil {
		return err
	}

	// Mark started only after Enable() succeeded — if Enable fails,
	// Started() returns false and callers skip Summary() (avoiding
	// an all-zero snapshot that misleads consumers).
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if e.Request != nil {
				atomic.AddInt64(&m.inflight, 1)
			}
		case *network.EventResponseReceived:
			m.recordResponse(e)
		case *network.EventLoadingFinished:
			m.recordFinalSize(e.RequestID, int64(e.EncodedDataLength))
			atomic.AddInt64(&m.inflight, -1)
			atomic.AddInt64(&m.completed, 1)
		case *network.EventLoadingFailed:
			// Failed requests (DNS error, timeout, connection refused) never
			// fire EventLoadingFinished — without this handler inflight would
			// leak on every failed request, making IsIdle() return false forever.
			atomic.AddInt64(&m.inflight, -1)
			atomic.AddInt64(&m.completed, 1)
		}
	})
	return nil
}

// recordResponse extracts auth-failure and summary data from a CDP response
// event. Called from the ListenTarget goroutine.
func (m *NetworkMonitor) recordResponse(e *network.EventResponseReceived) {
	if e.Response == nil {
		return
	}
	status := int(e.Response.Status)
	url := e.Response.URL
	m.lastActivityAt.Store(time.Now().UnixMilli())

	m.mu.Lock()
	defer m.mu.Unlock()

	// Record for summary (bounded).
	if len(m.requests) < maxRecordedRequests {
		// Method isn't on the Response event; we capture it best-effort from
		// the request via a separate listener below is overkill — leave blank,
		// status+url is what diagnostics need.
		m.requests = append(m.requests, NetworkRequest{
			URL:          url,
			Status:       status,
			RequestID:    string(e.RequestID),
			MimeType:     e.Response.MimeType,
			ResourceType: string(e.Type),
		})
	}

	// Auth failures: 401 (Unauthorized) and 403 (Forbidden). These are the
	// strongest signal that the page needs credentials the scraper doesn't have.
	if status == 401 || status == 403 {
		m.authFailures = append(m.authFailures, AuthFailure{URL: url, Status: status})
	}

	// CDN/blocking heuristics. Intentionally conservative — we flag, we don't
	// verdict. The LLM judges from the summary.
	if status == 429 || status == 503 {
		m.blockedCDN = true
	}
}

// recordFinalSize updates EncodedDataLength for a request when its body has
// finished loading. EventResponseReceived only carries a partial size; the
// authoritative total arrives on EventLoadingFinished. Called from the
// ListenTarget goroutine.
func (m *NetworkMonitor) recordFinalSize(reqID network.RequestID, size int64) {
	if reqID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.requests {
		if m.requests[i].RequestID == string(reqID) {
			m.requests[i].EncodedDataLength = size
			return
		}
	}
}

// ContentCandidateFilter defines which captured responses are worth surfacing
// to the LLM as potential content sources (#83). These are responses that
// likely carry the page's real content in a form the DOM doesn't reflect —
// the classic "lazy-loaded SPA" pattern where the API returns 87KB of JSON
// but the DOM stays thin.
type ContentCandidateFilter struct {
	// MinSize is the minimum response body size in bytes. Responses smaller
	// than this are ignored — they're almost certainly not the main content.
	// Default: 2048 (2KB).
	MinSize int64

	// MimeTypes lists acceptable MIME types (prefix match). Defaults to
	// application/json and text/.
	MimeTypes []string
}

// DefaultContentCandidateFilter returns the standard filter: XHR/Fetch
// responses, JSON or text, at least 2KB, status 200.
func DefaultContentCandidateFilter() ContentCandidateFilter {
	return ContentCandidateFilter{
		MinSize:   2048,
		MimeTypes: []string{"application/json", "text/"},
	}
}

// ContentCandidate is a response that likely carries page content in a format
// the DOM doesn't reflect (e.g. a lazy-loaded SPA API call). Surfaced when the
// scraper detects that the DOM is thin relative to API responses (#83).
type ContentCandidate struct {
	URL               string `json:"url"`
	MimeType          string `json:"mime_type,omitempty"`
	EncodedDataLength int64  `json:"encoded_data_length,omitempty"`
}

// matchesContentFilter tests a single recorded request against the content
// candidate filter. Shared by ContentCandidates and ContentCandidateRequestIDs
// to keep the filter logic in one place.
func matchesContentFilter(r NetworkRequest, filter ContentCandidateFilter) bool {
	if r.Status != 200 {
		return false
	}
	// Only XHR/Fetch — Document, Script, Image etc. are not content APIs.
	if r.ResourceType != string(network.ResourceTypeXHR) &&
		r.ResourceType != string(network.ResourceTypeFetch) {
		return false
	}
	if r.EncodedDataLength < filter.MinSize {
		return false
	}
	for _, mt := range filter.MimeTypes {
		if strings.HasPrefix(r.MimeType, mt) {
			return true
		}
	}
	return false
}

// ContentCandidates returns API/XHR responses that might carry the page's real
// content. Filters on ResourceType (XHR/Fetch only), status 200, MIME type, and
// minimum body size. Returns a copy; safe to call after the scrape.
//
// This is the detection half of #83: when these candidates collectively dwarf
// the rendered DOM, the content is in the API, not the DOM. The caller decides
// whether to capture bodies (GetResponseBody) based on this signal.
func (m *NetworkMonitor) ContentCandidates(filter ContentCandidateFilter) []ContentCandidate {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []ContentCandidate
	for _, r := range m.requests {
		if !matchesContentFilter(r, filter) {
			continue
		}
		out = append(out, ContentCandidate{
			URL:               r.URL,
			MimeType:          r.MimeType,
			EncodedDataLength: r.EncodedDataLength,
		})
	}
	return out
}

// ContentCandidateRequestIDs returns the CDP request IDs for responses that
// match the content-candidate filter. Needed by CaptureResponseBodies to fetch
// response bodies via network.GetResponseBody after the page has loaded.
func (m *NetworkMonitor) ContentCandidateRequestIDs(filter ContentCandidateFilter) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ids []string
	for _, r := range m.requests {
		if !matchesContentFilter(r, filter) {
			continue
		}
		if r.RequestID != "" {
			ids = append(ids, r.RequestID)
		}
	}
	return ids
}

// Started reports whether the CDP listener was successfully registered.
// Callers that captured a *NetworkMonitor but may have skipped Start (e.g. it
// errored) should guard Summary() with this to avoid emitting an all-zero
// summary that misleads consumers.
func (m *NetworkMonitor) Started() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// Inflight returns the current count of started-but-not-finished requests.
// Used for idle detection: inflight==0 for N ms ≈ network idle.
func (m *NetworkMonitor) Inflight() int64 {
	return atomic.LoadInt64(&m.inflight)
}

// IsIdle reports whether the network has been quiet (inflight==0) for at least
// quietFor. This is the event-driven replacement for the Performance-API
// polling in NetworkIdleAdvanced — no Evaluate round-trips, no guessing from
// performance entries.
func (m *NetworkMonitor) IsIdle(quietFor time.Duration) bool {
	if atomic.LoadInt64(&m.inflight) > 0 {
		return false
	}
	last := m.lastActivityAt.Load()
	if last == 0 {
		return true // no activity yet — treat as idle
	}
	return time.Since(time.UnixMilli(last)) >= quietFor
}

// Summary returns a snapshot of the observed network activity for diagnostics
// metadata. Safe to call after the scrape; the returned slice is a copy.
func (m *NetworkMonitor) Summary() NetworkSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	reqs := make([]NetworkRequest, len(m.requests))
	copy(reqs, m.requests)
	authFails := make([]AuthFailure, len(m.authFailures))
	copy(authFails, m.authFailures)

	return NetworkSummary{
		TotalRequests: atomic.LoadInt64(&m.completed),
		AuthFailures:  authFails,
		BlockedByCDN:  m.blockedCDN,
		Requests:      reqs,
	}
}

// Stop is a no-op safety net. The CDP listener is bound to the chromedp context
// and exits when that context is canceled (scrape ends). Provided for explicit
// lifecycle clarity and future use (e.g. detaching the listener early).
func (m *NetworkMonitor) Stop() {}

// RecordResponseForTest is a test helper that simulates a CDP response event
// with the given parameters. It records both the response and the final size
// (as if LoadingFinished fired). For unit tests only — production code uses
// the ListenTarget event handler.
func (m *NetworkMonitor) RecordResponseForTest(reqID, url string, status int, mimeType, resourceType string, finalSize int64) {
	m.recordResponse(&network.EventResponseReceived{
		RequestID: network.RequestID(reqID),
		Type:      network.ResourceType(resourceType),
		Response: &network.Response{
			URL:      url,
			Status:   int64(status),
			MimeType: mimeType,
		},
	})
	m.recordFinalSize(network.RequestID(reqID), finalSize)
}

// NetworkSummary is the diagnostics-facing snapshot of observed network
// activity. Returned in scrape metadata so the LLM can see what the page
// actually did (auth failures, blocking, API calls) without guessing.
type NetworkSummary struct {
	TotalRequests int64            `json:"total_requests"`
	AuthFailures  []AuthFailure    `json:"auth_failures,omitempty"`
	BlockedByCDN  bool             `json:"blocked_by_cdn,omitempty"`
	Requests      []NetworkRequest `json:"requests,omitempty"`
}

// AuthFailureSummary returns a compact human-readable hint for the LLM based on
// observed auth failures, e.g. "POST /api/auth → 401". Returns "" if none.
// Bounded to 3 entries to keep the hint small.
func (s NetworkSummary) AuthFailureHint() string {
	if len(s.AuthFailures) == 0 {
		return ""
	}
	var parts []string
	for i, f := range s.AuthFailures {
		if i >= 3 {
			parts = append(parts, "...")
			break
		}
		// Trim long URLs to their path for readability.
		u := f.URL
		if i := strings.Index(u, "://"); i >= 0 {
			u = u[i+3:]
			if j := strings.Index(u, "/"); j >= 0 {
				u = u[j:]
			}
		}
		parts = append(parts, u+" → "+itoa(f.Status))
	}
	return strings.Join(parts, ", ")
}

// itoa avoids importing strconv just for this one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
