package browser

import (
	"testing"

	"github.com/chromedp/cdproto/network"
)

// TestContentCandidatesFiltering verifies the core #83 detection signal:
// XHR/Fetch responses with JSON/text MIME, status 200, and size >= threshold
// are flagged as content candidates. Non-matching responses are excluded.
func TestContentCandidatesFiltering(t *testing.T) {
	m := NewNetworkMonitor()
	filter := DefaultContentCandidateFilter()

	// Large JSON XHR response — should be a candidate (simulates rebrainme API).
	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-1",
		Type:      network.ResourceTypeXHR,
		Response: &network.Response{
			URL:      "https://x.com/api/v2/tasks/3706",
			Status:   200,
			MimeType: "application/json",
		},
	})
	m.recordFinalSize("req-1", 87206)

	// Small JSON XHR — below 2KB threshold, not a candidate.
	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-2",
		Type:      network.ResourceTypeXHR,
		Response: &network.Response{
			URL:      "https://x.com/api/v2/config",
			Status:   200,
			MimeType: "application/json",
		},
	})
	m.recordFinalSize("req-2", 500)

	// Large text Fetch response — should be a candidate.
	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-3",
		Type:      network.ResourceTypeFetch,
		Response: &network.Response{
			URL:      "https://x.com/api/content",
			Status:   200,
			MimeType: "text/html",
		},
	})
	m.recordFinalSize("req-3", 15000)

	// Image response — not a content candidate (wrong resource type).
	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-4",
		Type:      network.ResourceTypeImage,
		Response: &network.Response{
			URL:      "https://x.com/logo.png",
			Status:   200,
			MimeType: "image/png",
		},
	})
	m.recordFinalSize("req-4", 50000)

	// 401 JSON response — not a candidate (non-200 status).
	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-5",
		Type:      network.ResourceTypeXHR,
		Response: &network.Response{
			URL:      "https://x.com/api/v2/secure",
			Status:   401,
			MimeType: "application/json",
		},
	})
	m.recordFinalSize("req-5", 10000)

	// Script response — not a candidate (wrong resource type + mime).
	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-6",
		Type:      network.ResourceTypeScript,
		Response: &network.Response{
			URL:      "https://x.com/app.js",
			Status:   200,
			MimeType: "application/javascript",
		},
	})
	m.recordFinalSize("req-6", 50000)

	candidates := m.ContentCandidates(filter)

	if len(candidates) != 2 {
		t.Fatalf("ContentCandidates = %d, want 2 (JSON XHR + text Fetch)", len(candidates))
	}

	// Verify the large JSON API response is captured.
	foundAPI := false
	foundText := false
	for _, c := range candidates {
		if c.URL == "https://x.com/api/v2/tasks/3706" {
			foundAPI = true
			if c.EncodedDataLength != 87206 {
				t.Errorf("API candidate size = %d, want 87206", c.EncodedDataLength)
			}
		}
		if c.URL == "https://x.com/api/content" {
			foundText = true
		}
	}
	if !foundAPI {
		t.Error("large JSON XHR should be a content candidate")
	}
	if !foundText {
		t.Error("large text Fetch should be a content candidate")
	}
}

// TestContentCandidateRequestIDs verifies that request IDs are returned for
// matching candidates — needed by CaptureResponseBodies to fetch bodies.
func TestContentCandidateRequestIDs(t *testing.T) {
	m := NewNetworkMonitor()
	filter := DefaultContentCandidateFilter()

	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-api-1",
		Type:      network.ResourceTypeXHR,
		Response: &network.Response{
			URL:      "https://x.com/api/tasks",
			Status:   200,
			MimeType: "application/json",
		},
	})
	m.recordFinalSize("req-api-1", 50000)

	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-api-2",
		Type:      network.ResourceTypeFetch,
		Response: &network.Response{
			URL:      "https://x.com/api/data",
			Status:   200,
			MimeType: "application/json",
		},
	})
	m.recordFinalSize("req-api-2", 30000)

	ids := m.ContentCandidateRequestIDs(filter)
	if len(ids) != 2 {
		t.Fatalf("ContentCandidateRequestIDs = %d, want 2", len(ids))
	}

	// Verify IDs are correct.
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	if !idSet["req-api-1"] || !idSet["req-api-2"] {
		t.Errorf("missing request IDs; got %v", ids)
	}
}

// TestRecordFinalSize verifies that EncodedDataLength from LoadingFinished
// updates the request record. EventResponseReceived may carry a partial size;
// the authoritative total arrives on LoadingFinished.
func TestRecordFinalSize(t *testing.T) {
	m := NewNetworkMonitor()

	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-x",
		Type:      network.ResourceTypeXHR,
		Response: &network.Response{
			URL:      "https://x.com/api/big",
			Status:   200,
			MimeType: "application/json",
		},
	})

	// Before LoadingFinished, size should be 0.
	if req := m.findRequestByID("req-x"); req == nil || req.EncodedDataLength != 0 {
		t.Error("expected 0 EncodedDataLength before LoadingFinished")
	}

	// After LoadingFinished, size should be updated.
	m.recordFinalSize("req-x", 4242)
	if req := m.findRequestByID("req-x"); req == nil || req.EncodedDataLength != 4242 {
		t.Errorf("expected EncodedDataLength=4242 after LoadingFinished, got %d", req.EncodedDataLength)
	}
}

// TestNetworkRequestExtendedFields verifies the new fields (MimeType,
// ResourceType, RequestID) are recorded from CDP events.
func TestNetworkRequestExtendedFields(t *testing.T) {
	m := NewNetworkMonitor()

	m.recordResponse(&network.EventResponseReceived{
		RequestID: "req-ext",
		Type:      network.ResourceTypeFetch,
		Response: &network.Response{
			URL:      "https://x.com/api/data",
			Status:   200,
			MimeType: "application/json",
		},
	})

	req := m.findRequestByID("req-ext")
	if req == nil {
		t.Fatal("request not found")
	}
	if req.MimeType != "application/json" {
		t.Errorf("MimeType = %q, want application/json", req.MimeType)
	}
	if req.ResourceType != string(network.ResourceTypeFetch) {
		t.Errorf("ResourceType = %q, want Fetch", req.ResourceType)
	}
	if req.RequestID != "req-ext" {
		t.Errorf("RequestID = %q, want req-ext", req.RequestID)
	}
}

// TestContentCandidatesEmpty verifies that a monitor with no matching
// responses returns nil (not an empty slice) — callers rely on len()==0.
func TestContentCandidatesEmpty(t *testing.T) {
	m := NewNetworkMonitor()
	filter := DefaultContentCandidateFilter()

	// Only non-matching responses.
	m.recordResponse(&network.EventResponseReceived{
		Type: network.ResourceTypeDocument,
		Response: &network.Response{
			URL:      "https://x.com/",
			Status:   200,
			MimeType: "text/html",
		},
	})
	m.recordFinalSize("d1", 5000)

	candidates := m.ContentCandidates(filter)
	if candidates != nil {
		t.Errorf("expected nil candidates, got %v", candidates)
	}
}
