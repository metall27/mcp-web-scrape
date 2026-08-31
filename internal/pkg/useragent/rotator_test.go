package useragent

import (
	"strings"
	"testing"
)

// TestGetRandomDesktopChromeOnly verifies the #95 P0 fix: the desktop pick
// must only ever return a plain desktop Chrome UA — no Firefox, no Safari,
// no Edge, no Headless, no mobile — because both consumers (Chrome scraper
// on a Chromium engine, HTTP scraper with a Chrome uTLS ClientHello) would
// contradict a non-Chrome UA.
func TestGetRandomDesktopChromeOnly(t *testing.T) {
	r := New(Config{})
	for i := 0; i < 500; i++ {
		ua := r.GetRandomDesktop()
		l := strings.ToLower(ua)
		switch {
		case !strings.Contains(l, "chrome"):
			t.Fatalf("non-Chrome UA returned: %s", ua)
		case strings.Contains(l, "headless"):
			t.Fatalf("headless UA returned: %s", ua)
		case strings.Contains(l, "edg/"):
			t.Fatalf("Edge UA returned: %s", ua)
		case strings.Contains(l, "firefox"):
			t.Fatalf("Firefox UA returned: %s", ua)
		case strings.Contains(l, "gecko/20100101"): // Firefox marker without the word
			t.Fatalf("Firefox UA returned: %s", ua)
		case !strings.Contains(l, "chrome/") && strings.Contains(l, "safari") && !strings.Contains(l, "chrome"):
			t.Fatalf("Safari UA returned: %s", ua)
		case strings.Contains(l, "mobile") || strings.Contains(l, "android") || strings.Contains(l, "iphone"):
			t.Fatalf("mobile UA returned: %s", ua)
		}
	}
}

// TestGetMatchesDesktopChromeOnly: Get() feeds the HTTP/uTLS scraper whose
// ClientHello is Chrome 120 — same Chrome-only restriction applies.
func TestGetMatchesDesktopChromeOnly(t *testing.T) {
	r := New(Config{})
	for i := 0; i < 500; i++ {
		ua := r.Get()
		l := strings.ToLower(ua)
		if !strings.Contains(l, "chrome") || strings.Contains(l, "edg/") ||
			strings.Contains(l, "headless") || strings.Contains(l, "gecko/20100101") {
			t.Fatalf("Get() returned non-desktop-Chrome UA: %s", ua)
		}
	}
}

// TestChromeOnlyFallsBackOnEmptyList: a custom config with no usable UA must
// still return a valid Chrome UA, never an empty string.
func TestChromeOnlyFallsBackOnEmptyList(t *testing.T) {
	r := New(Config{CustomUserAgents: []string{"some-custom-ua-without-chrome"}})
	// Defaults are always included, so this exercises the filter, not the
	// empty branch; construct the empty case directly.
	r2 := &Rotator{}
	ua := r2.GetRandomDesktop()
	if ua == "" || !strings.Contains(strings.ToLower(ua), "chrome") {
		t.Fatalf("empty-rotator fallback returned %q", ua)
	}
	_ = r
}
