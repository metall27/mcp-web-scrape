package browser

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// newTestPool builds a browser pool with named-session support enabled, for
// use by the localStorage snapshot tests. No browser is launched — the Chrome
// process starts lazily on the first chromedp.Run, which these tests never do.
func newTestPool(t *testing.T) *Pool {
	t.Helper()
	pool, err := New(Config{
		Logger:     zerolog.Nop(),
		MaxTabs:    2,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// TestLocalStorageSnapshotGetEmpty verifies that GetCachedLocalStorage returns
// nil before any snapshot has been saved, and for a session that does not
// exist. It must NEVER launch Chrome (pure map read).
func TestLocalStorageSnapshotGetEmpty(t *testing.T) {
	pool := newTestPool(t)
	sm := pool.Sessions()

	if got := sm.GetCachedLocalStorage("nope"); got != nil {
		t.Errorf("GetCachedLocalStorage(nonexistent) = %v, want nil", got)
	}

	if _, err := sm.GetOrCreate(pool.Allocator(), "snap-empty", "UA", BrowserFingerprint{}); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got := sm.GetCachedLocalStorage("snap-empty"); got != nil {
		t.Errorf("GetCachedLocalStorage(fresh session) = %v, want nil (no snapshot yet)", got)
	}
}

// TestLocalStorageSnapshotSaveGet verifies the save→get round-trip: after
// SaveLocalStorage, GetCachedLocalStorage returns the same key/value pairs.
func TestLocalStorageSnapshotSaveGet(t *testing.T) {
	pool := newTestPool(t)
	sm := pool.Sessions()

	if _, err := sm.GetOrCreate(pool.Allocator(), "snap-a", "UA", BrowserFingerprint{}); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	values := map[string]string{
		"auth_token":   "eyJ-secret",
		"persist:root": `{"user":"alice"}`,
		"theme":        "dark",
	}
	sm.SaveLocalStorage("snap-a", values)

	got := sm.GetCachedLocalStorage("snap-a")
	if got == nil {
		t.Fatal("GetCachedLocalStorage returned nil after SaveLocalStorage")
	}
	if len(got) != len(values) {
		t.Fatalf("snapshot size = %d, want %d", len(got), len(values))
	}
	for k, v := range values {
		if got[k] != v {
			t.Errorf("snapshot[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestLocalStorageSnapshotReturnsCopy verifies that the map returned by
// GetCachedLocalStorage is a COPY — mutating it must not affect the session's
// stored snapshot. Without this, a caller in the scrape path could corrupt
// the snapshot for subsequent scrapes.
func TestLocalStorageSnapshotReturnsCopy(t *testing.T) {
	pool := newTestPool(t)
	sm := pool.Sessions()

	if _, err := sm.GetOrCreate(pool.Allocator(), "snap-copy", "UA", BrowserFingerprint{}); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	sm.SaveLocalStorage("snap-copy", map[string]string{"k1": "v1", "k2": "v2"})

	got := sm.GetCachedLocalStorage("snap-copy")
	got["k1"] = "MUTATED"
	delete(got, "k2")
	got["injected"] = "x"

	again := sm.GetCachedLocalStorage("snap-copy")
	if again["k1"] != "v1" {
		t.Errorf("snapshot mutated through returned copy: k1 = %q, want v1", again["k1"])
	}
	if _, ok := again["k2"]; !ok {
		t.Errorf("snapshot key k2 was deleted through returned copy")
	}
	if _, ok := again["injected"]; ok {
		t.Errorf("snapshot gained 'injected' key through returned copy")
	}
}

// TestLocalStorageSnapshotOverwrite verifies that a second SaveLocalStorage
// replaces the previous snapshot entirely (not a merge).
func TestLocalStorageSnapshotOverwrite(t *testing.T) {
	pool := newTestPool(t)
	sm := pool.Sessions()

	if _, err := sm.GetOrCreate(pool.Allocator(), "snap-ow", "UA", BrowserFingerprint{}); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	sm.SaveLocalStorage("snap-ow", map[string]string{"old": "1"})
	sm.SaveLocalStorage("snap-ow", map[string]string{"new": "2"})

	got := sm.GetCachedLocalStorage("snap-ow")
	if _, ok := got["old"]; ok {
		t.Errorf("snapshot still contains 'old' after overwrite")
	}
	if got["new"] != "2" {
		t.Errorf("snapshot['new'] = %q, want '2'", got["new"])
	}
}

// TestLocalStorageSnapshotAfterClose verifies that closing a session makes the
// snapshot inaccessible (SaveLocalStorage/GetCachedLocalStorage are no-ops on
// a closed/missing session).
func TestLocalStorageSnapshotAfterClose(t *testing.T) {
	pool := newTestPool(t)
	sm := pool.Sessions()

	if _, err := sm.GetOrCreate(pool.Allocator(), "snap-close", "UA", BrowserFingerprint{}); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	sm.SaveLocalStorage("snap-close", map[string]string{"k": "v"})

	if !sm.Close("snap-close") {
		t.Fatal("Close returned false")
	}

	if got := sm.GetCachedLocalStorage("snap-close"); got != nil {
		t.Errorf("GetCachedLocalStorage after Close = %v, want nil", got)
	}
	// SaveLocalStorage on a closed session must be a silent no-op (no panic).
	sm.SaveLocalStorage("snap-close", map[string]string{"late": "write"})
}
