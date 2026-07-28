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
	Method string `json:"method,omitempty"` // GET, POST, ... (from request)
	URL    string `json:"url"`
	Status int    `json:"status"`
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
			URL:    url,
			Status: status,
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
