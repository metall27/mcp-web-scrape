package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/metall/mcp-web-scrape/internal/pkg/config"
)

// TestHTTPFallbackRejectsSmallContentAfterActionError is the core regression
// test for issue #49: when Chrome failed on a user-supplied action (login) and
// the HTTP fallback returns a suspiciously small body, the scraper must return
// an error instead of masquerading junk as a successful scrape.
//
// We stand up an httptest server that returns a tiny stub body (simulating the
// redirect/error page a login-gated site serves without session cookies) and
// call httpFallback directly with reason=fallbackReasonActionError.
func TestHTTPFallbackRejectsSmallContentAfterActionError(t *testing.T) {
	// Tiny body — well below minFallbackContentSize (200). Simulates the
	// 51-byte redirect stub from the rebrainme case.
	stubBody := `<html><body>redirect</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubBody))
	}))
	defer srv.Close()

	scraper := NewChromeScraper(nil, nil, config.RAGConfig{}, config.BrowserConfig{}, nil, nil, config.GitHubConfig{})
	scraper.logger = zerolog.Nop()

	result, err := scraper.httpFallback(
		context.Background(),
		srv.URL,
		"TestUA/1.0",
		fallbackReasonActionError,
		time.Now(),
	)

	if err == nil {
		t.Fatalf("expected error for small content after action_error, got nil result (result=%+v)", result)
	}

	// Must be a *ScrapeError with the fallback_action_error code.
	var se *ScrapeError
	if !errors.As(err, &se) {
		t.Fatalf("expected *ScrapeError, got %T: %v", err, err)
	}
	if se.Code != "fallback_action_error" {
		t.Errorf("Code = %q, want fallback_action_error", se.Code)
	}
	// Message should mention the size so the client understands the signal.
	if !strings.Contains(se.Message, "51 bytes") && !strings.Contains(se.Message, "bytes") {
		t.Errorf("Message should mention byte size, got: %s", se.Message)
	}
}

// TestHTTPFallbackAcceptsLargeContentAfterActionError confirms the guard is
// specific to SMALL content: a large body after an action error still returns
// a Result (with a fallback warning) rather than erroring — the content might
// be legitimately small-after-redirect but still usable, and the client gets
// the warning to judge for itself.
func TestHTTPFallbackAcceptsLargeContentAfterActionError(t *testing.T) {
	// Large body — above the threshold. Even after an action_error, this is
	// returned as a Result (with a warning), not rejected.
	largeBody := strings.Repeat("a", minFallbackContentSize*5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(largeBody))
	}))
	defer srv.Close()

	scraper := NewChromeScraper(nil, nil, config.RAGConfig{}, config.BrowserConfig{}, nil, nil, config.GitHubConfig{})
	scraper.logger = zerolog.Nop()

	result, err := scraper.httpFallback(
		context.Background(),
		srv.URL,
		"TestUA/1.0",
		fallbackReasonActionError,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("expected success for large content, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Must carry the fallback provenance so the client gets a warning.
	if result.FallbackReason != fallbackReasonActionError {
		t.Errorf("FallbackReason = %q, want %q", result.FallbackReason, fallbackReasonActionError)
	}
	if result.FallbackWarning == "" {
		t.Error("FallbackWarning should be non-empty when reason is set")
	}
}

// TestHTTPFallbackSmallContentAllowedForOtherReasons confirms the rejection is
// specific to action_error. A small body from a generic/blocking fallback is
// NOT rejected — only action_error carries the "no session cookies" signal.
func TestHTTPFallbackSmallContentAllowedForOtherReasons(t *testing.T) {
	cases := []string{fallbackReasonBlocking, fallbackReasonGeneric}
	for _, reason := range cases {
		t.Run(reason, func(t *testing.T) {
			stubBody := `<html><body>x</body></html>`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(stubBody))
			}))
			defer srv.Close()

			scraper := NewChromeScraper(nil, nil, config.RAGConfig{}, config.BrowserConfig{}, nil, nil, config.GitHubConfig{})
			scraper.logger = zerolog.Nop()

			result, err := scraper.httpFallback(
				context.Background(),
				srv.URL,
				"TestUA/1.0",
				reason,
				time.Now(),
			)
			if err != nil {
				t.Fatalf("expected success for reason=%s even with small content, got: %v", reason, err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.FallbackReason != reason {
				t.Errorf("FallbackReason = %q, want %q", result.FallbackReason, reason)
			}
		})
	}
}

// TestHTTPFallbackSetsProvenanceOnSuccess verifies that every successful
// fallback Result carries the reason + warning so the client always knows it
// got an HTTP body, not a JS-rendered page (variant B from the issue).
func TestHTTPFallbackSetsProvenanceOnSuccess(t *testing.T) {
	largeBody := strings.Repeat("b", minFallbackContentSize+50)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(largeBody))
	}))
	defer srv.Close()

	scraper := NewChromeScraper(nil, nil, config.RAGConfig{}, config.BrowserConfig{}, nil, nil, config.GitHubConfig{})
	scraper.logger = zerolog.Nop()

	result, err := scraper.httpFallback(
		context.Background(),
		srv.URL,
		"TestUA/1.0",
		fallbackReasonBlocking,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.FallbackReason == "" {
		t.Error("FallbackReason should be set on a successful fallback Result")
	}
	if result.FallbackWarning == "" {
		t.Error("FallbackWarning should be set on a successful fallback Result")
	}
}
