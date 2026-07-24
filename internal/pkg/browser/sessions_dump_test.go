package browser

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestSessionDumpNotFound verifies Dump returns (nil, false) for a session
// that does not exist — no panics, no Chrome launch needed.
func TestSessionDumpNotFound(t *testing.T) {
	logger := zerolog.Nop()
	pool, err := New(Config{
		Logger:     logger,
		MaxTabs:    2,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	sm := pool.Sessions()
	if sm == nil {
		t.Fatal("Sessions() returned nil — named sessions not enabled")
	}

	dump, ok := sm.Dump(context.Background(), "nonexistent", false)
	if ok {
		t.Fatal("Dump for nonexistent session should return ok=false")
	}
	if dump != nil {
		t.Errorf("Dump for nonexistent session should return nil dump, got %+v", dump)
	}
}

// TestSessionList verifies List returns metadata for active sessions without
// requiring Chrome. Sessions are created (context derived from allocator) but
// never run — the browser process starts lazily on the first chromedp.Run.
func TestSessionList(t *testing.T) {
	logger := zerolog.Nop()
	pool, err := New(Config{
		Logger:     logger,
		MaxTabs:    2,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	sm := pool.Sessions()

	// Create two sessions with distinct identities.
	uaA := "Mozilla/5.0 (Session-List-A)"
	fpA := BrowserFingerprint{Platform: "Win32"}
	if _, err := sm.GetOrCreate(pool.Allocator(), "list-A", uaA, fpA); err != nil {
		t.Fatalf("GetOrCreate A: %v", err)
	}
	uaB := "Mozilla/5.0 (Session-List-B)"
	fpB := BrowserFingerprint{Platform: "MacIntel"}
	if _, err := sm.GetOrCreate(pool.Allocator(), "list-B", uaB, fpB); err != nil {
		t.Fatalf("GetOrCreate B: %v", err)
	}

	sessions := sm.List()
	if len(sessions) != 2 {
		t.Fatalf("List returned %d sessions, want 2", len(sessions))
	}

	// Build a map for lookup — order is non-deterministic (map iteration).
	byID := make(map[string]SessionDump, len(sessions))
	for _, s := range sessions {
		byID[s.SessionID] = s
	}

	for _, id := range []string{"list-A", "list-B"} {
		s, ok := byID[id]
		if !ok {
			t.Errorf("session %q missing from List output", id)
			continue
		}
		// List must NOT include cookies/storage (those require Chrome) —
		// only metadata. CookieCount should be zero.
		if s.CookieCount != 0 {
			t.Errorf("session %q: CookieCount = %d, want 0 (List must not query Chrome)", id, s.CookieCount)
		}
	}

	// Verify the pinned UA is reflected.
	if byID["list-A"].UserAgent != uaA {
		t.Errorf("list-A UA = %q, want %q", byID["list-A"].UserAgent, uaA)
	}
	if byID["list-B"].UserAgent != uaB {
		t.Errorf("list-B UA = %q, want %q", byID["list-B"].UserAgent, uaB)
	}

	// Created/LastAccess must be set.
	if byID["list-A"].Created.IsZero() {
		t.Error("list-A: Created is zero")
	}
	if byID["list-A"].LastAccess.IsZero() {
		t.Error("list-A: LastAccess is zero")
	}
}

// TestSessionListEmpty confirms List returns an empty (not nil) slice when no
// sessions exist — callers iterating the result must not panic.
func TestSessionListEmpty(t *testing.T) {
	logger := zerolog.Nop()
	pool, err := New(Config{
		Logger:     logger,
		MaxTabs:    1,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	sessions := pool.Sessions().List()
	if sessions == nil {
		t.Fatal("List returned nil, want empty slice")
	}
	if len(sessions) != 0 {
		t.Errorf("List returned %d sessions, want 0", len(sessions))
	}
}

// TestSessionDumpGracefulDegradation verifies that Dump on a session whose
// browser context was never launched (no chromedp.Run yet) degrades
// gracefully: the CDP calls fail, but Dump still returns the session metadata
// (created/lastAccess/UA) with empty cookies/storage rather than panicking.
// This mirrors the #42 debug use case where a caller inspects a freshly-created
// session before any navigation has happened.
func TestSessionDumpGracefulDegradation(t *testing.T) {
	logger := zerolog.Nop()
	pool, err := New(Config{
		Logger:     logger,
		MaxTabs:    1,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	sm := pool.Sessions()
	ua := "Mozilla/5.0 (Dump-Degraded)"
	fp := BrowserFingerprint{Platform: "Linux"}
	if _, err := sm.GetOrCreate(pool.Allocator(), "degraded", ua, fp); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	// Use a short timeout so a stuck Chrome launch doesn't hang the test.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	dump, ok := sm.Dump(ctx, "degraded", false)
	if !ok {
		t.Fatal("Dump returned ok=false for an existing session")
	}
	if dump == nil {
		t.Fatal("Dump returned nil dump for an existing session")
	}

	// Metadata must be present even though CDP failed.
	if dump.SessionID != "degraded" {
		t.Errorf("SessionID = %q, want degraded", dump.SessionID)
	}
	if dump.UserAgent != ua {
		t.Errorf("UserAgent = %q, want %q", dump.UserAgent, ua)
	}
	if dump.Created.IsZero() {
		t.Error("Created is zero — metadata should be present even on CDP failure")
	}

	// Cookies/storage may be empty (Chrome unreachable) — that's the graceful
	// degradation. The important contract: no panic, metadata returned.
	// Non-nil slices/maps are acceptable; nil is also fine for the "no data"
	// case. We only assert the counts are consistent with the slice lengths.
	if dump.CookieCount != len(dump.Cookies) {
		t.Errorf("CookieCount (%d) != len(Cookies) (%d)", dump.CookieCount, len(dump.Cookies))
	}
}
