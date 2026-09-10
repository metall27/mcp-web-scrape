package browser

import (
	"strings"
	"testing"
)

// Stage 3 of #107: deterministic per-profile media fingerprints.

func TestProfileSeedStableForSameProfile(t *testing.T) {
	p1 := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	p2 := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	// NewProfile randomizes locale/webgl — force identical pinned parts.
	p2.Timezone, p2.Language = p1.Timezone, p1.Language
	p2.WebGLVendor, p2.WebGLRenderer = p1.WebGLVendor, p1.WebGLRenderer
	if profileSeed(p1) != profileSeed(p2) {
		t.Error("same pinned identity must yield the same seed")
	}
}

func TestProfileSeedDiffersAcrossIdentities(t *testing.T) {
	base := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	mac := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	a, b := NewProfile(base), NewProfile(mac)
	if profileSeed(a) == profileSeed(b) {
		t.Error("different platforms must yield different seeds")
	}
}

// The session-restore path (ProfileFromParts) must reproduce the exact same
// seed — the whole stage 3 premise: restart of a named session keeps the
// media fingerprint.
func TestProfileSeedSurvivesProfileFromPartsRoundTrip(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	p := NewProfile(ua)
	fp := p.Fingerprint()
	restored := ProfileFromParts(ua, fp)
	if profileSeed(p) != profileSeed(restored) {
		t.Errorf("seed changed after ProfileFromParts round-trip: %d != %d", profileSeed(p), profileSeed(restored))
	}
}

func TestDeterministicMediaScriptShape(t *testing.T) {
	p := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	js := buildDeterministicMediaScript(p)

	if strings.Contains(js, "__MEDIA_SEED__") {
		t.Error("seed placeholder not spliced")
	}
	if !strings.Contains(js, "mcpwsDisguise") {
		t.Error("script must fetch disguise() via the Symbol registry (ordering contract)")
	}
	for _, native := range []string{
		"function toDataURL() { [native code] }",
		"function getImageData() { [native code] }",
		"function getChannelData() { [native code] }",
	} {
		if !strings.Contains(js, native) {
			t.Errorf("missing disguise text %q", native)
		}
	}
	// #117: the OffscreenCanvas wrappers must be present — its context
	// prototype does not inherit from CanvasRenderingContext2D.
	if !strings.Contains(js, "OffscreenCanvasRenderingContext2D") {
		t.Error("OffscreenCanvas getImageData wrapper missing")
	}
	if !strings.Contains(js, "convertToBlob") {
		t.Error("OffscreenCanvas convertToBlob wrapper missing")
	}
	// Check the JS body only (strip Go line comments — they document the
	// retired Math.random approach and legitimately mention it).
	body := js
	if i := strings.Index(body, "		(() => {"); i >= 0 {
		body = body[i:]
	}
	if strings.Contains(body, "Math.random") {
		t.Error("Math.random must not appear in the deterministic media script body")
	}
	// Alpha channel must be preserved (comment states RGB-only flips).
	if !strings.Contains(js, "RGB only") && !strings.Contains(js, "LSB flips on RGB") {
		t.Error("RGB-only noise not documented in script")
	}
}

func TestCombinedScriptIncludesDeterministicMedia(t *testing.T) {
	sa := NewStealthActions(StealthConfig{})
	p := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	combined := sa.BuildCombinedStealthScript(p)

	if !strings.Contains(combined, "buildDeterministicMediaScript") &&
		!strings.Contains(combined, "noisyCopy") {
		t.Error("combined stealth script does not include the deterministic media script")
	}
	// Ordering: identity script (disguise installer) BEFORE media script.
	idIdx := strings.Index(combined, "globalThis[Symbol.for('mcpwsDisguise')] =")
	mediaIdx := strings.Index(combined, "noisyCopy")
	if idIdx < 0 || mediaIdx < 0 || idIdx > mediaIdx {
		t.Errorf("ordering broken: disguise installer at %d, media script at %d", idIdx, mediaIdx)
	}
}

// No Math.random-based canvas/audio/font noise may remain anywhere in the
// composed registration (behavioral jitter excluded — it lives in its own
// section and mimics human movement, not fingerprint values).
func TestAdvancedNoiseSectionsRetired(t *testing.T) {
	adv := NewAdvancedStealth()
	for name, js := range map[string]string{
		"canvas": adv.CanvasAntiFingerprinting(),
		"audio":  adv.AudioAntiFingerprinting(),
		"font":   adv.FontAntiFingerprinting(),
	} {
		if strings.Contains(js, "Math.random") {
			t.Errorf("%s section still contains Math.random noise", name)
		}
	}
}
