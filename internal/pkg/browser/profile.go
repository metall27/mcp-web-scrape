package browser

import (
	"hash/fnv"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"

	"github.com/metall/mcp-web-scrape/internal/pkg/geo"
)

// BrowserProfile is the single source of truth for the browser identity the
// Chrome scraper advertises: HTTP User-Agent header, Sec-CH-UA client hints
// (Emulation.setUserAgentOverride metadata), navigator.platform,
// timezone/locale and WebGL strings (#95).
//
// Previously UA, platform, brands and WebGL were chosen INDEPENDENTLY, which
// produced contradictions a bot detector flags instantly:
//   - Firefox UA from the rotator + Chromium Sec-CH-UA brands from the engine;
//   - Windows UA + MacIntel navigator.platform;
//   - WebGL vendor "Google Inc. (Intel)" paired with an AMD renderer.
//
// All values in a BrowserProfile are generated together and stay coherent.
// The profile is also DETERMINISTIC for a given (UserAgent, BrowserFingerprint)
// pair, so a named session re-derives the exact same identity on every call.
type BrowserProfile struct {
	// UserAgent is the full UA string sent in the HTTP User-Agent header and
	// returned by navigator.userAgent (via Emulation.setUserAgentOverride —
	// never contains "HeadlessChrome").
	UserAgent string

	// Platform is navigator.platform / Emulation platform ("Win32",
	// "MacIntel", "Linux x86_64"), always consistent with UserAgent.
	Platform string

	// CHPlatform is the Sec-CH-UA-Platform value ("Windows", "macOS",
	// "Linux", "Android").
	CHPlatform string

	// PlatformVersion is the Sec-CH-UA-Platform-Version value.
	PlatformVersion string

	// Architecture / Bitness are Sec-CH-UA-Arch / Sec-CH-UA-Bitness values.
	Architecture string
	Bitness      string

	// Mobile mirrors the UA's mobile-ness (Sec-CH-UA-Mobile).
	Mobile bool

	// ChromeMajor / ChromeFull are the parsed Chrome versions used for the
	// Sec-CH-UA brand list ("124" / "124.0.0.0").
	ChromeMajor string
	ChromeFull  string

	// Timezone / Language are a coherent IANA-timezone + locale pair
	// (e.g. "de-DE" + "Europe/Berlin", never "ja-JP" + "America/New_York").
	// TimezoneLongName is the human-readable zone name a real Chrome prints
	// in Date.prototype.toString(), e.g. "(Eastern Daylight Time)".
	Timezone         string
	TimezoneLongName string
	Language         string

	// WebGLVendor / WebGLRenderer are a VALID GPU pair (Intel vendor with an
	// Intel GPU, etc.) appropriate for the profile's OS.
	WebGLVendor   string
	WebGLRenderer string

	// HardwareConcurrency / DeviceMemory are deterministic for a given UA
	// (stable across reloads and session reuse — real hardware does not
	// change between page loads).
	HardwareConcurrency int
	DeviceMemory        int

	// Screen dimensions (deterministic per UA, avail* accounts for a taskbar).
	ScreenWidth  int
	ScreenHeight int
	AvailWidth   int
	AvailHeight  int
}

// profileRnd is the shared randomness source for profile generation.
var profileRnd = rand.New(rand.NewSource(time.Now().UnixNano()))
var profileRndMu sync.Mutex

func pickRandom(n int) int {
	profileRndMu.Lock()
	defer profileRndMu.Unlock()
	return profileRnd.Intn(n)
}

// localeTimezones pairs a locale with a matching IANA timezone. Locale and
// timezone must agree or Intl.DateTimeFormat betrays the JS overrides.
// longName is what a real Chrome prints in Date.prototype.toString(), e.g.
// "Mon Jan 01 2026 10:00:00 GMT-0400 (Eastern Daylight Time)".
var localeTimezones = []struct {
	Language string
	Timezone string
	LongName string
}{
	{"en-US", "America/New_York", "Eastern Daylight Time"},
	{"en-US", "America/Los_Angeles", "Pacific Daylight Time"},
	{"en-GB", "Europe/London", "Greenwich Mean Time"},
	{"de-DE", "Europe/Berlin", "Central European Summer Time"},
	{"fr-FR", "Europe/Paris", "Central European Summer Time"},
	{"es-ES", "Europe/Madrid", "Central European Summer Time"},
	{"ja-JP", "Asia/Tokyo", "Japan Standard Time"},
}

// webglPairs maps navigator.platform to valid GPU vendor/renderer pairs.
// Vendor and renderer are picked TOGETHER — mixing an Intel vendor with an
// AMD renderer is an instant fingerprint mismatch (#95).
var webglPairs = map[string][][2]string{
	"Win32": {
		{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 SUPER Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 580 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
	},
	"MacIntel": {
		{"Google Inc. (Intel)", "ANGLE (Intel Inc., Intel(R) Iris(TM) Plus Graphics 655, OpenGL 4.1)"},
		{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon Pro 5500M OpenGL Engine, OpenGL 4.1)"},
	},
	"Linux x86_64": {
		{"Google Inc. (Intel)", "ANGLE (Intel, Mesa Intel(R) UHD Graphics (CML GT2), OpenGL 4.6)"},
		{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 580 (polaris10), OpenGL 4.6)"},
	},
}

// defaultWebGLPair is used for platforms without an explicit pair table.
var defaultWebGLPair = [2]string{
	"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630, OpenGL 4.6)",
}

var chromeVersionRe = regexp.MustCompile(`Chrome/(\d+)(?:\.(\d+)\.(\d+)\.(\d+))?`)

// NewProfile builds a coherent random profile around the given User-Agent
// string. The UA is normalized (HeadlessChrome → Chrome) and everything else
// (platform, brands, timezone/locale, WebGL pair, hardware) is derived from
// or matched to it. Hardware values are deterministic per UA so they stay
// stable across page reloads.
//
// When a geo.Locale is passed, its language/timezone OVERRIDE the random
// locale pick: the profile then agrees with the geography of the egress IP,
// which is what anti-bot systems check ("de-DE from a Kazan IP" is an
// instant VPN/bot flag, #99). A nil/zero Locale keeps the legacy behavior.
func NewProfile(userAgent string, geoLocale ...geo.Locale) BrowserProfile {
	p := parseUserAgent(userAgent)

	if len(geoLocale) > 0 && geoLocale[0].Language != "" && geoLocale[0].Timezone != "" {
		p.Timezone = geoLocale[0].Timezone
		p.Language = geoLocale[0].Language
		p.TimezoneLongName = timezoneLongName(geoLocale[0].Timezone)
	} else {
		lt := localeTimezones[pickRandom(len(localeTimezones))]
		p.Timezone = lt.Timezone
		p.TimezoneLongName = lt.LongName
		p.Language = lt.Language
	}

	pair := webglPairFor(p.Platform)
	if pair == nil {
		pair = &defaultWebGLPair
	}
	p.WebGLVendor = pair[0]
	p.WebGLRenderer = pair[1]

	p.applyDeterministicHardware()
	return p
}

// ProfileFromParts re-derives the profile for a User-Agent + pinned
// BrowserFingerprint pair (named sessions). Timezone, language and WebGL come
// from the fingerprint so a reused session advertises the exact identity it
// was created with; the rest is deterministic from the UA. Calling this twice
// with the same inputs returns identical profiles.
func ProfileFromParts(userAgent string, fp BrowserFingerprint) BrowserProfile {
	p := parseUserAgent(userAgent)

	p.Timezone = fp.Timezone
	p.Language = fp.Language
	p.TimezoneLongName = timezoneLongName(fp.Timezone)
	if p.Timezone == "" {
		p.Timezone = "America/New_York"
		p.TimezoneLongName = "Eastern Daylight Time"
	}
	if p.Language == "" {
		p.Language = "en-US"
	}

	if fp.WebGLVendor != "" && fp.WebGLRenderer != "" {
		p.WebGLVendor = fp.WebGLVendor
		p.WebGLRenderer = fp.WebGLRenderer
	} else {
		pair := webglPairFor(p.Platform)
		if pair == nil {
			pair = &defaultWebGLPair
		}
		p.WebGLVendor, p.WebGLRenderer = pair[0], pair[1]
	}

	p.applyDeterministicHardware()
	return p
}

// Fingerprint converts the profile to the BrowserFingerprint consumed by the
// stealth JS injection (timezone, language, platform, WebGL).
func (p BrowserProfile) Fingerprint() BrowserFingerprint {
	return BrowserFingerprint{
		ViewportWidth:  p.ScreenWidth,
		ViewportHeight: p.ScreenHeight,
		Timezone:       p.Timezone,
		Language:       p.Language,
		Platform:       p.Platform,
		WebGLVendor:    p.WebGLVendor,
		WebGLRenderer:  p.WebGLRenderer,
	}
}

// UserAgentMetadata builds the Sec-CH-UA metadata for
// Emulation.setUserAgentOverride. This kills "HeadlessChrome" in Sec-CH-UA
// and navigator.userAgentData in one shot — the HTTP header, the client-hint
// headers and navigator.userAgentData all come from the same override.
func (p BrowserProfile) UserAgentMetadata() *emulation.UserAgentMetadata {
	full := p.ChromeFull
	if full == "" {
		full = p.ChromeMajor + ".0.0.0"
	}
	brands := []*emulation.UserAgentBrandVersion{
		{Brand: "Chromium", Version: p.ChromeMajor},
		{Brand: "Google Chrome", Version: p.ChromeMajor},
		{Brand: "Not:A-Brand", Version: "8"},
	}
	fullBrands := []*emulation.UserAgentBrandVersion{
		{Brand: "Chromium", Version: full},
		{Brand: "Google Chrome", Version: full},
		{Brand: "Not:A-Brand", Version: "8"},
	}
	return &emulation.UserAgentMetadata{
		Brands:          brands,
		FullVersionList: fullBrands,
		Platform:        p.CHPlatform,
		PlatformVersion: p.PlatformVersion,
		Architecture:    p.Architecture,
		Model:           "",
		Mobile:          p.Mobile,
		Bitness:         p.Bitness,
	}
}

// AcceptLanguage returns the Accept-Language / navigator.language value.
func (p BrowserProfile) AcceptLanguage() string {
	if p.Language != "" {
		return p.Language
	}
	return "en-US"
}

// localeICU converts the BCP-47 language tag ("en-US") to the ICU C-locale
// form ("en_US") required by Emulation.setLocaleOverride.
func (p BrowserProfile) LocaleICU() string {
	return strings.ReplaceAll(p.AcceptLanguage(), "-", "_")
}

// parseUserAgent normalizes the UA (HeadlessChrome → Chrome) and derives the
// platform identity deterministically from the UA string itself, so
// navigator.platform can never contradict the UA.
func parseUserAgent(userAgent string) BrowserProfile {
	ua := strings.ReplaceAll(userAgent, "HeadlessChrome", "Chrome")

	p := BrowserProfile{UserAgent: ua}

	// Chrome version
	if m := chromeVersionRe.FindStringSubmatch(ua); m != nil {
		p.ChromeMajor = m[1]
		if m[2] != "" {
			p.ChromeFull = m[1] + "." + m[2] + "." + m[3] + "." + m[4]
		} else {
			p.ChromeFull = m[1] + ".0.0.0"
		}
	} else {
		p.ChromeMajor = "124"
		p.ChromeFull = "124.0.0.0"
	}

	// Platform — derived from the UA, never picked independently.
	switch {
	case strings.Contains(ua, "Windows NT"):
		p.Platform = "Win32"
		p.CHPlatform = "Windows"
		p.PlatformVersion = "13.0.0" // Windows 11 (build 22621)
		p.Architecture = "x86"
		p.Bitness = "64"
	case strings.Contains(ua, "Android"):
		p.Platform = "Linux armv8l"
		p.CHPlatform = "Android"
		p.PlatformVersion = "14.0.0"
		p.Architecture = "arm"
		p.Bitness = "64"
		p.Mobile = true
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		p.Platform = "iPhone"
		p.CHPlatform = "iOS"
		p.PlatformVersion = "17.4.0"
		p.Architecture = "arm"
		p.Bitness = "64"
		p.Mobile = true
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		p.Platform = "MacIntel"
		p.CHPlatform = "macOS"
		// Match the UA's OS version: the default rotator UAs are all
		// "Mac OS X 10_15_7" (Catalina), and real Chrome on 10.15.7 sends
		// Sec-CH-UA-Platform-Version "10.15.7" — not a Sonoma "13.5.0".
		if m := regexp.MustCompile(`Mac OS X (\d+)_(\d+)(?:_(\d+))?`).FindStringSubmatch(ua); m != nil {
			p.PlatformVersion = m[1] + "." + m[2]
			if m[3] != "" {
				p.PlatformVersion += "." + m[3]
			}
		} else {
			p.PlatformVersion = "10.15.7"
		}
		p.Architecture = "x86"
		p.Bitness = "64"
	default: // "Linux x86_64" and anything unrecognized
		p.Platform = "Linux x86_64"
		p.CHPlatform = "Linux"
		p.PlatformVersion = "6.5.0"
		p.Architecture = "x86"
		p.Bitness = "64"
	}

	return p
}

// applyDeterministicHardware derives hardwareConcurrency, deviceMemory and
// screen dimensions from a hash of the UA. Real hardware does not change
// between page reloads, so these values must be stable for a given UA —
// randomizing them per document is itself a detection signal (#95).
func (p *BrowserProfile) applyDeterministicHardware() {
	h := fnv.New32a()
	h.Write([]byte(p.UserAgent))
	v := int(h.Sum32())

	cores := []int{4, 8, 12, 16}
	p.HardwareConcurrency = cores[v%len(cores)]
	if p.HardwareConcurrency <= 4 {
		p.DeviceMemory = 8
	} else {
		p.DeviceMemory = 16
	}

	// Screen: 1920x1080 with a taskbar/dock-sized avail area.
	p.ScreenWidth = 1920
	p.ScreenHeight = 1080
	switch p.Platform {
	case "MacIntel":
		p.AvailWidth = 1920
		p.AvailHeight = 1055
	default:
		p.AvailWidth = 1920
		p.AvailHeight = 1040
	}
}

func webglPairFor(platform string) *[2]string {
	pairs, ok := webglPairs[platform]
	if !ok || len(pairs) == 0 {
		return nil
	}
	pair := pairs[pickRandom(len(pairs))]
	return &pair
}

// timezoneLongName maps an IANA timezone to the human-readable zone name a
// real Chrome prints in Date.prototype.toString(). Unknown zones fall back
// to a generic name derived from the IANA identifier.
func timezoneLongName(tz string) string {
	for _, lt := range localeTimezones {
		if lt.Timezone == tz {
			return lt.LongName
		}
	}
	// Generic fallback: "Europe/Berlin" -> "(Berlin Time)"-ish neutral name.
	parts := strings.Split(tz, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return strings.ReplaceAll(parts[len(parts)-1], "_", " ") + " Time"
	}
	return "Coordinated Universal Time"
}
