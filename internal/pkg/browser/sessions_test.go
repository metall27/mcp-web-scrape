package browser

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestSessionPinsUserAgentAndFingerprint verifies the core #41 contract:
// a named session pins its User-Agent and fingerprint at creation time.
// Subsequent GetOrCreate calls with different values must NOT overwrite
// the pinned identity — the first-write wins.
//
// No Chrome is launched: chromedp.NewContext against the pool's allocator
// only extends the context tree; the browser process starts lazily on the
// first chromedp.Run, which never happens here.
func TestSessionPinsUserAgentAndFingerprint(t *testing.T) {
	logger := zerolog.Nop()

	pool, err := New(Config{
		Logger:     logger,
		MaxTabs:    2,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30 * time.Minute, // enable named sessions
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	sm := pool.Sessions()
	if sm == nil {
		t.Fatal("Sessions() returned nil — named sessions not enabled")
	}

	ua1 := "Mozilla/5.0 (Session-One)"
	fp1 := BrowserFingerprint{
		ViewportWidth:  1920,
		ViewportHeight: 1080,
		Timezone:       "Europe/Berlin",
		Language:       "de-DE",
		Platform:       "Win32",
		WebGLVendor:    "Google Inc. (Intel)",
		WebGLRenderer:  "ANGLE (Intel)",
	}

	// First call creates the session and pins ua1/fp1.
	ctx1, err := sm.GetOrCreate(pool.Allocator(), "sess-A", ua1, fp1)
	if err != nil {
		t.Fatalf("GetOrCreate (create): %v", err)
	}
	if ctx1 == nil {
		t.Fatal("GetOrCreate returned nil context")
	}

	// Second call with different identity must reuse the session and
	// IGNORE the newly supplied ua2/fp2.
	ua2 := "Mozilla/5.0 (Session-Two-Different)"
	fp2 := BrowserFingerprint{
		Timezone:      "Asia/Tokyo",
		Language:      "ja-JP",
		Platform:      "MacIntel",
		WebGLVendor:   "Google Inc. (AMD)",
		WebGLRenderer: "ANGLE (AMD)",
	}
	_, err = sm.GetOrCreate(pool.Allocator(), "sess-A", ua2, fp2)
	if err != nil {
		t.Fatalf("GetOrCreate (reuse): %v", err)
	}

	gotUA, ok := sm.GetUserAgent("sess-A")
	if !ok {
		t.Fatal("GetUserAgent: session not found")
	}
	if gotUA != ua1 {
		t.Errorf("pinned UA: got %q, want %q (should not change between calls)", gotUA, ua1)
	}

	gotFP, ok := sm.GetFingerprint("sess-A")
	if !ok {
		t.Fatal("GetFingerprint: session not found")
	}
	if gotFP != fp1 {
		t.Errorf("pinned fingerprint changed between calls:\n got  %+v\n want %+v", gotFP, fp1)
	}
}

// TestSessionPinningPerSession verifies that different sessions get
// independent identities (no accidental cross-session sharing).
func TestSessionPinningPerSession(t *testing.T) {
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

	uaA := "UA-Alpha"
	fpA := BrowserFingerprint{Timezone: "Europe/London", Language: "en-GB", Platform: "Win32"}
	uaB := "UA-Beta"
	fpB := BrowserFingerprint{Timezone: "Asia/Tokyo", Language: "ja-JP", Platform: "MacIntel"}

	if _, err := sm.GetOrCreate(pool.Allocator(), "sess-A", uaA, fpA); err != nil {
		t.Fatalf("GetOrCreate A: %v", err)
	}
	if _, err := sm.GetOrCreate(pool.Allocator(), "sess-B", uaB, fpB); err != nil {
		t.Fatalf("GetOrCreate B: %v", err)
	}

	gotA, _ := sm.GetUserAgent("sess-A")
	gotB, _ := sm.GetUserAgent("sess-B")
	if gotA == gotB {
		t.Errorf("sessions A and B share the same UA: %q (should be independent)", gotA)
	}
	if gotA != uaA || gotB != uaB {
		t.Errorf("UA mismatch: A=%q want %q, B=%q want %q", gotA, uaA, gotB, uaB)
	}

	fpGotA, _ := sm.GetFingerprint("sess-A")
	fpGotB, _ := sm.GetFingerprint("sess-B")
	if fpGotA.Timezone == fpGotB.Timezone {
		t.Errorf("sessions A and B share timezone: %q (should be independent)", fpGotA.Timezone)
	}
}

// TestSessionGettersMissingSession verifies the getters return ok=false
// for a session that was never created or already closed.
func TestSessionGettersMissingSession(t *testing.T) {
	logger := zerolog.Nop()

	pool, err := New(Config{
		Logger:     logger,
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

	// Never created.
	if _, ok := sm.GetUserAgent("nope"); ok {
		t.Error("GetUserAgent returned ok for non-existent session")
	}
	if _, ok := sm.GetFingerprint("nope"); ok {
		t.Error("GetFingerprint returned ok for non-existent session")
	}

	// Create then close — getters must report gone.
	if _, err := sm.GetOrCreate(pool.Allocator(), "ephem", "UA", BrowserFingerprint{}); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if !sm.Close("ephem") {
		t.Fatal("Close returned false for existing session")
	}
	if _, ok := sm.GetUserAgent("ephem"); ok {
		t.Error("GetUserAgent returned ok after Close")
	}
	if _, ok := sm.GetFingerprint("ephem"); ok {
		t.Error("GetFingerprint returned ok after Close")
	}
}
