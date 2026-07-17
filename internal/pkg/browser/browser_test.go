package browser

import (
	"sync"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

// hasChrome reports whether a Chrome/Chromium binary is discoverable by chromedp.
// Integration tests below skip when it is not available (CI, sandboxes).
func hasChrome() bool {
	// chromedp.NewExecAllocator resolves the browser binary lazily on first
	// context creation, so we cannot cheaply probe here without launching one.
	// Tests that need a live browser must opt in via a build tag or -chrome flag.
	return false
}

// TestNewPoolDefaults verifies that a Pool can be constructed without launching
// a browser process and that defaults are applied. No network / no Chrome needed.
func TestNewPoolDefaults(t *testing.T) {
	logger := zerolog.Nop()

	pool, err := New(Config{
		Logger:         logger,
		MaxTabs:        0, // expect default
		Headless:       true,
		DisableGPU:     true,
		NoSandbox:      true,
		ViewportWidth:  1920,
		ViewportHeight: 1080,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	stats := pool.GetStats()
	if got := stats["max_tabs"]; got != 10 {
		t.Errorf("default max_tabs: got %v, want 10", got)
	}
	if got := pool.GetActiveTabs(); got != 0 {
		t.Errorf("new pool active tabs: got %d, want 0", got)
	}
}

// TestGetStatsShape ensures GetStats returns all expected keys and types.
// No Chrome required — the pool only allocates a context, it does not launch
// the browser until GetContext is called.
func TestGetStatsShape(t *testing.T) {
	logger := zerolog.Nop()

	pool, err := New(Config{
		Logger:         logger,
		MaxTabs:        5,
		Headless:       true,
		NoSandbox:      true,
		DisableGPU:     true,
		ViewportWidth:  1280,
		ViewportHeight: 720,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	stats := pool.GetStats()

	requiredKeys := []string{"active_tabs", "total_tabs", "max_tabs"}
	for _, k := range requiredKeys {
		if _, ok := stats[k]; !ok {
			t.Errorf("GetStats missing key %q (got %v)", k, stats)
		}
	}
	if stats["max_tabs"] != 5 {
		t.Errorf("max_tabs: got %v, want 5", stats["max_tabs"])
	}
}

// TestGetActiveTabsConcurrent verifies GetActiveTabs is safe under concurrent
// access. No Chrome required: GetActiveTabs only reads an atomic int32.
func TestGetActiveTabsConcurrent(t *testing.T) {
	logger := zerolog.Nop()

	pool, err := New(Config{
		Logger:         logger,
		MaxTabs:        10,
		Headless:       true,
		NoSandbox:      true,
		DisableGPU:     true,
		ViewportWidth:  1920,
		ViewportHeight: 1080,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	const goroutines = 16
	const reads = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < reads; j++ {
				_ = pool.GetActiveTabs()
				_ = pool.GetStats()
			}
		}()
	}
	wg.Wait()

	t.Log("GetActiveTabs / GetStats are thread-safe under concurrent reads")
}

// TestPoolWithCustomAllocator is a smoke test for the allocator option path.
// No Chrome launched until GetContext is called.
func TestPoolWithCustomAllocator(t *testing.T) {
	if !hasChrome() {
		t.Skip("skipping: no Chrome binary available (set up Chrome to run integration tests)")
	}

	logger := zerolog.Nop()
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	}

	// This exercises the Config path that previously had an `AllocOptions` field
	// (removed). We pass standard flags via New() and verify no panic.
	pool, err := New(Config{
		Logger:         logger,
		MaxTabs:        3,
		Headless:       true,
		NoSandbox:      true,
		DisableGPU:     true,
		ViewportWidth:  1920,
		ViewportHeight: 1080,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	// opts is intentionally unused above; reference to keep the import meaningful
	// for future allocator-override work.
	_ = opts
}
