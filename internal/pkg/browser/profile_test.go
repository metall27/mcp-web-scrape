package browser

import (
	"strings"
	"testing"
)

// TestProfileNeverContainsHeadless verifies the #95 P0 acceptance criteria:
// the profile's UA must never contain "Headless" — not in the HTTP header UA,
// not in the client-hint brand list.
func TestProfileNeverContainsHeadless(t *testing.T) {
	headless := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/124.0.0.0 Safari/537.36"
	for i := 0; i < 50; i++ {
		p := NewProfile(headless)
		if strings.Contains(p.UserAgent, "Headless") {
			t.Fatalf("profile UA still contains Headless: %s", p.UserAgent)
		}
		for _, b := range p.UserAgentMetadata().Brands {
			if strings.Contains(b.Brand, "Headless") {
				t.Fatalf("brand contains Headless: %+v", b)
			}
		}
	}
}

// TestProfilePlatformMatchesUA verifies navigator.platform can never
// contradict the UA string (Windows UA → Win32, Mac UA → MacIntel, etc.).
func TestProfilePlatformMatchesUA(t *testing.T) {
	cases := []struct {
		ua       string
		platform string
		ch       string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36", "Win32", "Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36", "MacIntel", "macOS"},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36", "Linux x86_64", "Linux"},
		{"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.6367.0 Mobile Safari/537.36", "Linux armv8l", "Android"},
	}
	for _, c := range cases {
		p := NewProfile(c.ua)
		if p.Platform != c.platform {
			t.Errorf("UA %q: platform = %q, want %q", c.ua, p.Platform, c.platform)
		}
		if p.CHPlatform != c.ch {
			t.Errorf("UA %q: CH platform = %q, want %q", c.ua, p.CHPlatform, c.ch)
		}
		if p.Mobile != (c.ch == "Android") {
			t.Errorf("UA %q: mobile = %v, want %v", c.ua, p.Mobile, c.ch == "Android")
		}
	}
}

// TestProfileWebGLPairIsValid verifies vendor and renderer are picked
// TOGETHER: an Intel vendor string must pair with an Intel GPU, NVIDIA with
// NVIDIA, AMD with AMD (#95 item 7).
func TestProfileWebGLPairIsValid(t *testing.T) {
	gpuOf := func(s string) string {
		switch {
		case strings.Contains(s, "Intel"):
			return "intel"
		case strings.Contains(s, "NVIDIA"):
			return "nvidia"
		case strings.Contains(s, "AMD"), strings.Contains(s, "Radeon"):
			return "amd"
		}
		return "unknown"
	}
	for i := 0; i < 100; i++ {
		p := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		if gpuOf(p.WebGLVendor) != gpuOf(p.WebGLRenderer) {
			t.Fatalf("mismatched WebGL pair: vendor=%q renderer=%q", p.WebGLVendor, p.WebGLRenderer)
		}
	}
}

// TestProfileDeterministicHardware verifies hardwareConcurrency/deviceMemory
// and screen dimensions are stable for the same UA — real hardware does not
// change between page reloads (#95 item 3).
func TestProfileDeterministicHardware(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	first := NewProfile(ua)
	for i := 0; i < 20; i++ {
		p := NewProfile(ua)
		if p.HardwareConcurrency != first.HardwareConcurrency ||
			p.DeviceMemory != first.DeviceMemory ||
			p.ScreenWidth != first.ScreenWidth ||
			p.ScreenHeight != first.ScreenHeight ||
			p.AvailWidth != first.AvailWidth ||
			p.AvailHeight != first.AvailHeight {
			t.Fatalf("hardware fingerprint unstable for same UA:\nfirst=%+v\ngot  =%+v", first, p)
		}
	}
}

// TestProfileFromPartsStableForSession verifies a named session re-derives
// the identical profile from its pinned (UA, fingerprint) pair on every call.
func TestProfileFromPartsStableForSession(t *testing.T) {
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	fp := BrowserFingerprint{
		Timezone:      "Europe/Berlin",
		Language:      "de-DE",
		Platform:      "MacIntel",
		WebGLVendor:   "Google Inc. (AMD)",
		WebGLRenderer: "ANGLE (AMD, AMD Radeon Pro 5500M OpenGL Engine, OpenGL 4.1)",
	}
	a := ProfileFromParts(ua, fp)
	b := ProfileFromParts(ua, fp)
	if a != b {
		t.Fatalf("ProfileFromParts not deterministic:\n%+v\n%+v", a, b)
	}
	if a.Timezone != fp.Timezone || a.Language != fp.Language {
		t.Fatalf("pinned TZ/locale not honored: %+v", a)
	}
	if a.WebGLVendor != fp.WebGLVendor || a.WebGLRenderer != fp.WebGLRenderer {
		t.Fatalf("pinned WebGL not honored: %+v", a)
	}
	if a.Platform != "MacIntel" {
		t.Fatalf("platform %q contradicts Mac UA", a.Platform)
	}
}

// TestProfileBrandVersions verifies the Sec-CH-UA brand list shape:
// Chromium + Google Chrome with the UA's major version, plus Not:A-Brand.
func TestProfileBrandVersions(t *testing.T) {
	p := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	if p.ChromeMajor != "123" {
		t.Fatalf("ChromeMajor = %q, want 123", p.ChromeMajor)
	}
	md := p.UserAgentMetadata()
	if len(md.Brands) != 3 {
		t.Fatalf("brands = %+v, want 3 entries", md.Brands)
	}
	if md.Brands[0].Brand != "Chromium" || md.Brands[1].Brand != "Google Chrome" {
		t.Fatalf("unexpected brands: %+v", md.Brands)
	}
	if md.FullVersionList[1].Version != "123.0.0.0" {
		t.Fatalf("full version = %q, want 123.0.0.0", md.FullVersionList[1].Version)
	}
}

// TestProfileLocaleTimezoneCoherent verifies the locale and IANA timezone are
// picked as a pair (de-DE never pairs with America/New_York).
func TestProfileLocaleTimezoneCoherent(t *testing.T) {
	valid := map[string]string{
		"en-US": "America/", "en-GB": "Europe/London", "de-DE": "Europe/Berlin",
		"fr-FR": "Europe/Paris", "es-ES": "Europe/Madrid", "ja-JP": "Asia/Tokyo",
	}
	for i := 0; i < 100; i++ {
		p := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		prefix, ok := valid[p.Language]
		if !ok {
			t.Fatalf("unexpected locale %q", p.Language)
		}
		if !strings.HasPrefix(p.Timezone, prefix) && p.Timezone != "America/Los_Angeles" {
			t.Fatalf("locale %q paired with timezone %q", p.Language, p.Timezone)
		}
	}
}
