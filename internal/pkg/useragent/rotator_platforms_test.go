package useragent

import (
	"strings"
	"testing"
)

// TestGetRandomDesktopForPlatforms (#112): with the Win32-only allowance
// (the current font-reference state) every random pick must advertise
// Windows; with an empty allowance the full desktop pool is back.
func TestGetRandomDesktopForPlatforms(t *testing.T) {
	r := New(Config{})

	for i := 0; i < 60; i++ {
		ua := r.GetRandomDesktopForPlatforms([]string{"Win32"})
		if !strings.Contains(ua, "Windows NT") {
			t.Fatalf("pick %d not a Windows UA: %s", i, ua)
		}
		if !strings.Contains(ua, "Chrome/") || strings.Contains(ua, "Edg/") {
			t.Fatalf("pick %d not a plain desktop Chrome UA: %s", i, ua)
		}
	}

	// Mac allowance picks Mac UAs only.
	for i := 0; i < 60; i++ {
		ua := r.GetRandomDesktopForPlatforms([]string{"MacIntel"})
		if !strings.Contains(ua, "Macintosh") && !strings.Contains(ua, "Mac OS X") {
			t.Fatalf("pick %d not a Mac UA: %s", i, ua)
		}
	}

	// Both platforms: mix allowed, still desktop Chrome.
	for i := 0; i < 60; i++ {
		ua := r.GetRandomDesktopForPlatforms([]string{"Win32", "MacIntel"})
		if !strings.Contains(ua, "Chrome/") || strings.Contains(ua, "Edg/") ||
			strings.Contains(ua, "Mobile") || strings.Contains(ua, "Android") {
			t.Fatalf("pick %d not a desktop Chrome UA: %s", i, ua)
		}
	}

	// Empty allowance: unrestricted (both Win and Mac possible over many
	// draws — just assert it never returns empty or a non-Chrome UA).
	for i := 0; i < 60; i++ {
		ua := r.GetRandomDesktopForPlatforms(nil)
		if ua == "" || !strings.Contains(ua, "Chrome/") {
			t.Fatalf("pick %d bad unrestricted UA: %q", i, ua)
		}
	}

	// Unsupported platform label: falls back to unrestricted pool.
	ua := r.GetRandomDesktopForPlatforms([]string{"SunOS"})
	if ua == "" || !strings.Contains(ua, "Chrome/") {
		t.Fatalf("fallback pick bad: %q", ua)
	}
}
