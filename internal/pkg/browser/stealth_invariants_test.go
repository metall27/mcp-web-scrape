package browser

import (
	"fmt"
	"strings"
	"testing"
)

// TestStealthScriptInvariants verifies the generated anti-detection scripts
// satisfy the structural fixes of #95 Stage 2 — the exact classes of bugs
// that made the old stealth itself a detection signal.
func TestStealthScriptInvariants(t *testing.T) {
	sa := NewStealthActions(StealthConfig{})
	adv := NewAdvancedStealth()
	profile := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	main := sa.buildAntiDetectionScript(profile)
	identity := buildIdentityHardeningScript(profile)
	hw := adv.HardwareAntiFingerprinting(profile)
	screen := adv.ScreenAntiFingerprinting(profile)
	behavioral := adv.BehavioralAntiFingerprinting()

	// 1. getTimezoneOffset must be a FUNCTION, not a getter (#95 item 1):
	// the old `defineProperty(Date.prototype, 'getTimezoneOffset', {get: () => n})`
	// made `new Date().getTimezoneOffset()` throw TypeError.
	if !strings.Contains(main, "Date.prototype.getTimezoneOffset = function()") {
		t.Error("getTimezoneOffset is not overridden as a function")
	}
	if strings.Contains(main, "'getTimezoneOffset', {\n\t\t\t\tget:") {
		t.Error("getTimezoneOffset still uses a getter override")
	}

	// 2. Date.toString must resolve the zone name DYNAMICALLY via Intl
	// (#95 review: a hardcoded "Daylight" name contradicted the winter
	// offset); the profile value is only a fallback.
	if !strings.Contains(main, "timezoneLongNameFor(") {
		t.Error("Date.toString override does not resolve the zone name dynamically")
	}
	if !strings.Contains(main, fmt.Sprintf("%q", profile.TimezoneLongName)) {
		t.Errorf("fallback zone name %q not embedded in script", profile.TimezoneLongName)
	}
	if strings.Contains(main, "tzOffsetCache") {
		t.Error("offset cache survives — goes stale across DST transitions in long-lived sessions")
	}

	// 3. window.screen must NOT be replaced with a fresh object per access
	// (#95 item 2): the getter-returning-literal pattern is gone, properties
	// are redefined on the existing object instead.
	if strings.Contains(screen, "defineProperty(window, 'screen'") {
		t.Error("window.screen is still replaced via a window-level getter")
	}
	if !strings.Contains(screen, "defineProperty(window.screen,") {
		t.Error("screen properties are not redefined on the existing object")
	}

	// 4. Date.now must NOT be overridden at all (#95 item 4): any jitter
	// breaks Number.isInteger(Date.now()). Only performance.now may jitter.
	if strings.Contains(behavioral, "Date.now = function") {
		t.Error("Date.now is still overridden (integer guarantee broken)")
	}
	if !strings.Contains(behavioral, "performance.now = function") {
		t.Error("performance.now jitter missing")
	}

	// 5. clientX/clientY getter must not read e.clientX inside itself
	// (#95 item 5) — the values are captured before defineProperty.
	if strings.Contains(behavioral, "get: () => e.clientX") {
		t.Error("recursive clientX getter still present")
	}

	// 6. Hardware values come from the profile (#95 item 3), stable per UA —
	// not Math.random() per document.
	if !strings.Contains(hw, fmt.Sprintf("get: () => %d", profile.HardwareConcurrency)) {
		t.Errorf("hardwareConcurrency %d not pinned in script", profile.HardwareConcurrency)
	}
	if strings.Contains(hw, "Math.random()") {
		t.Error("hardware values still randomized per document")
	}

	// 7. window.chrome mock: members live in the identity script now
	// (#101 stage 5); the main script must NOT assign window.chrome anymore.
	if strings.Contains(main, "window.chrome =") {
		t.Error("main script still assigns window.chrome (moved to identity script in #101 stage 5)")
	}
	for _, member := range []string{"csi:", "loadTimes:"} {
		if !strings.Contains(identity, member) {
			t.Errorf("identity script window.chrome mock missing %s", member)
		}
	}
}

// TestTimezoneLongNameCoversProfiles verifies every locale/timezone pair
// carries a long name and unknown zones get a sane fallback.
func TestTimezoneLongNameCoversProfiles(t *testing.T) {
	for _, lt := range localeTimezones {
		if lt.LongName == "" {
			t.Errorf("localeTimezones entry %q has empty LongName", lt.Timezone)
		}
	}
	if got := timezoneLongName("Europe/Berlin"); got != "Central European Summer Time" {
		t.Errorf("timezoneLongName(Europe/Berlin) = %q", got)
	}
	if got := timezoneLongName("Australia/Sydney"); got != "Sydney Time" {
		t.Errorf("fallback for unknown zone = %q, want 'Sydney Time'", got)
	}
}
