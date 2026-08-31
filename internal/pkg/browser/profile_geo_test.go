package browser

import (
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/geo"
)

// Geo locale override (#99): when a geo.Locale is passed, the profile's
// language/timezone must come from it — the locale then agrees with the
// egress-IP geography instead of a random pick.
func TestNewProfileGeoLocaleOverride(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	p := NewProfile(ua, geo.Locale{Country: "RU", Timezone: "Europe/Moscow", Language: "ru-RU"})

	if p.Language != "ru-RU" {
		t.Errorf("Language = %q, want ru-RU", p.Language)
	}
	if p.Timezone != "Europe/Moscow" {
		t.Errorf("Timezone = %q, want Europe/Moscow", p.Timezone)
	}
	if p.TimezoneLongName == "" {
		t.Error("TimezoneLongName empty for geo-derived timezone")
	}
	if p.AcceptLanguage() != "ru-RU" {
		t.Errorf("AcceptLanguage = %q, want ru-RU", p.AcceptLanguage())
	}
	if p.LocaleICU() != "ru_RU" {
		t.Errorf("LocaleICU = %q, want ru_RU", p.LocaleICU())
	}
	// The UA-derived parts must be untouched by the geo override.
	if p.Platform != "Win32" || p.UserAgent != ua {
		t.Errorf("geo override leaked into UA-derived fields: %+v", p)
	}
}

// Zero geo.Locale keeps the legacy random-locale behavior (backward
// compatibility for callers and tests without network).
func TestNewProfileZeroGeoKeepsLegacy(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	p := NewProfile(ua)
	if p.Language == "" || p.Timezone == "" {
		t.Error("legacy random locale missing")
	}
	// Must be one of the known coherent pairs.
	found := false
	for _, lt := range localeTimezones {
		if lt.Language == p.Language && lt.Timezone == p.Timezone {
			found = true
		}
	}
	if !found {
		t.Errorf("legacy locale (%q/%q) not a coherent pair from localeTimezones", p.Language, p.Timezone)
	}
}

// Partial geo.Locale (language without timezone) is ignored entirely — a
// half-applied override would recreate the very mismatch #99 fixes.
func TestNewProfilePartialGeoIgnored(t *testing.T) {
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	p := NewProfile(ua, geo.Locale{Language: "ru-RU"}) // no Timezone
	found := false
	for _, lt := range localeTimezones {
		if lt.Language == p.Language && lt.Timezone == p.Timezone {
			found = true
		}
	}
	if !found {
		t.Errorf("partial geo locale applied: %q/%q — must fall back to a coherent pair", p.Language, p.Timezone)
	}
}

// Deterministic per (UA, geo) — two profiles for the same inputs advertise
// the same identity (stability across retries).
func TestNewProfileGeoDeterministic(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	g := geo.Locale{Country: "RU", Timezone: "Europe/Moscow", Language: "ru-RU"}
	p1 := NewProfile(ua, g)
	p2 := NewProfile(ua, g)
	if p1.Language != p2.Language || p1.Timezone != p2.Timezone || p1.TimezoneLongName != p2.TimezoneLongName {
		t.Errorf("geo profiles differ: %+v vs %+v", p1, p2)
	}
}
