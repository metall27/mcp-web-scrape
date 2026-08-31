package geo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- countryLocales mapping ---

func TestLanguageForCountry(t *testing.T) {
	cases := map[string]string{
		"RU": "ru-RU", "ru": "ru-RU", "De": "de-DE",
		"US": "en-US", "GB": "en-GB", "JP": "ja-JP",
		"XX": "", "": "", "R": "",
	}
	for in, want := range cases {
		if got := LanguageForCountry(in); got != want {
			t.Errorf("LanguageForCountry(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocaleForCountryRemoved(t *testing.T) {
	// LocaleForCountry was dead production code (review L1 on PR #100):
	// nothing in production called it. Guard against reintroduction
	// without a caller — grep the package source.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "LocaleForCountry") {
			t.Errorf("%s references LocaleForCountry — dead code reintroduced without a production caller", f)
		}
	}
}

// RU is the primary use case (#99) — it MUST be in the table.
func TestRUPresent(t *testing.T) {
	if LanguageForCountry("RU") != "ru-RU" {
		t.Fatal("RU missing from countryLocales — the #99 headline case")
	}
}

// --- parseProxyURL ---

func TestParseProxyURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"http://1.2.3.4:8080", "http://1.2.3.4:8080"},
		{"1.2.3.4:8080", "http://1.2.3.4:8080"},
		{"socks5://u:p@1.2.3.4:1080", "socks5://u:p@1.2.3.4:1080"},
	} {
		u, err := parseProxyURL(c.in)
		if err != nil {
			t.Fatalf("parseProxyURL(%q) error: %v", c.in, err)
		}
		got := ""
		if u != nil {
			got = u.String()
		}
		if got != c.want {
			t.Errorf("parseProxyURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- CachedResolver ---

type fakeResolver struct {
	calls  int
	locale Locale
	err    error
}

func (f *fakeResolver) Resolve(ctx context.Context, proxyURL string) (Locale, error) {
	f.calls++
	return f.locale, f.err
}

func TestCachedResolverCaches(t *testing.T) {
	inner := &fakeResolver{locale: Locale{Country: "RU", Timezone: "Europe/Moscow", Language: "ru-RU", Source: "resolver"}}
	c := NewCachedResolver(inner, time.Minute)

	l1, err := c.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	l2, err := c.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Errorf("inner resolver called %d times, want 1 (second hit must come from cache)", inner.calls)
	}
	if l1.Source != "resolver" {
		t.Errorf("first resolve source = %q, want resolver", l1.Source)
	}
	if l2.Source != "cache" {
		t.Errorf("second resolve source = %q, want cache", l2.Source)
	}
	if l2.Country != l1.Country || l2.Timezone != l1.Timezone || l2.Language != l1.Language {
		t.Errorf("cached locale fields drifted: %+v vs %+v", l2, l1)
	}
}

func TestCachedResolverKeysByEgress(t *testing.T) {
	inner := &fakeResolver{locale: Locale{Country: "RU", Language: "ru-RU", Timezone: "Europe/Moscow"}}
	c := NewCachedResolver(inner, time.Minute)

	_, _ = c.Resolve(context.Background(), "")
	_, _ = c.Resolve(context.Background(), "http://proxy:8080")
	_, _ = c.Resolve(context.Background(), "http://proxy:8080")

	if inner.calls != 2 {
		t.Errorf("inner calls = %d, want 2 (direct + proxy are separate cache keys)", inner.calls)
	}
}

func TestCachedResolverDoesNotCacheErrors(t *testing.T) {
	inner := &fakeResolver{err: errors.New("boom")}
	c := NewCachedResolver(inner, time.Minute)

	if _, err := c.Resolve(context.Background(), ""); err == nil {
		t.Fatal("expected error to propagate")
	}
	if _, err := c.Resolve(context.Background(), ""); err == nil {
		t.Fatal("expected error on second call too")
	}
	if inner.calls != 2 {
		t.Errorf("inner calls = %d, want 2 (errors must not be cached)", inner.calls)
	}
}

func TestCachedResolverTTLExpiry(t *testing.T) {
	inner := &fakeResolver{locale: Locale{Country: "RU", Language: "ru-RU", Timezone: "Europe/Moscow"}}
	c := NewCachedResolver(inner, time.Nanosecond)

	_, _ = c.Resolve(context.Background(), "")
	time.Sleep(2 * time.Millisecond)
	_, _ = c.Resolve(context.Background(), "")

	if inner.calls != 2 {
		t.Errorf("inner calls = %d, want 2 (expired entry must re-resolve)", inner.calls)
	}
}

// --- provider against a stub server (no network) ---

func stubProvider(t *testing.T, name, payload string, status int) (Provider, *httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			http.Error(w, payload, status)
			return
		}
		fmt.Fprintln(w, payload)
	}))
	t.Cleanup(srv.Close)
	return &jsonProvider{name: name, endpoint: srv.URL + "/json"}, srv, &hits
}

func TestProviderParses(t *testing.T) {
	p, _, _ := stubProvider(t, "stub", `{"ip":"95.105.4.122","country":"RU","timezone":"Europe/Moscow"}`, 0)
	loc, err := p.Resolve(context.Background(), &http.Client{}, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Country != "RU" || loc.Timezone != "Europe/Moscow" || loc.Language != "ru-RU" {
		t.Errorf("locale = %+v, want RU/Europe/Moscow/ru-RU", loc)
	}
	if loc.Source != "stub" {
		t.Errorf("source = %q, want stub", loc.Source)
	}
}

func TestProviderCountryCodeField(t *testing.T) {
	// ip-api style: countryCode instead of country
	p, _, _ := stubProvider(t, "stub", `{"status":"success","countryCode":"DE","timezone":"Europe/Berlin"}`, 0)
	loc, err := p.Resolve(context.Background(), &http.Client{}, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Country != "DE" || loc.Language != "de-DE" {
		t.Errorf("locale = %+v, want DE/de-DE", loc)
	}
}

func TestProviderUnknownCountry(t *testing.T) {
	p, _, _ := stubProvider(t, "stub", `{"country":"XX","timezone":"Nowhere/Foo"}`, 0)
	if _, err := p.Resolve(context.Background(), &http.Client{}, ""); err == nil {
		t.Error("expected error for unmapped country")
	}
}

func TestProviderHTTPError(t *testing.T) {
	p, _, _ := stubProvider(t, "stub", "rate limited", http.StatusTooManyRequests)
	if _, err := p.Resolve(context.Background(), &http.Client{}, ""); err == nil {
		t.Error("expected error on HTTP 429")
	}
}

func TestProviderStatusFailureIn200(t *testing.T) {
	// ip-api signals failure inside a 200 body
	p, _, _ := stubProvider(t, "stub", `{"status":"fail","message":"private range"}`, 0)
	if _, err := p.Resolve(context.Background(), &http.Client{}, ""); err == nil {
		t.Error("expected error for provider status=fail")
	}
}

// --- chainResolver: the dead-service concern ---

func TestChainSkipsDeadProvider(t *testing.T) {
	// First provider always 500s; the chain must answer from the second.
	dead, _, deadHits := stubProvider(t, "dead", "boom", http.StatusInternalServerError)
	alive, _, aliveHits := stubProvider(t, "alive", `{"country":"RU","timezone":"Europe/Moscow"}`, 0)
	chain := NewChainResolver([]Provider{dead, alive})

	loc, err := chain.Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("chain should survive a dead provider: %v", err)
	}
	if loc.Country != "RU" {
		t.Errorf("locale country = %q, want RU", loc.Country)
	}
	if *deadHits == 0 || *aliveHits == 0 {
		t.Error("expected both providers to be tried")
	}
}

func TestChainAllDead(t *testing.T) {
	dead, _, _ := stubProvider(t, "dead1", "x", 500)
	dead2, _, _ := stubProvider(t, "dead2", "y", 500)
	chain := NewChainResolver([]Provider{dead, dead2})
	if _, err := chain.Resolve(context.Background(), ""); err == nil {
		t.Error("expected error when all providers are dead")
	}
}

// --- static mode (use_external_geo_ip_discovery: false) ---

func TestStaticResolverRU(t *testing.T) {
	r := NewStaticResolver("RU")
	loc, err := r.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.Country != "RU" || loc.Language != "ru-RU" || loc.Timezone != "Europe/Moscow" {
		t.Errorf("static RU = %+v, want RU/ru-RU/Europe/Moscow", loc)
	}
	if loc.Source != "static" {
		t.Errorf("source = %q, want static", loc.Source)
	}
}

func TestStaticResolverUnmapped(t *testing.T) {
	if _, err := NewStaticResolver("XX").Resolve(context.Background(), ""); err == nil {
		t.Error("expected error for unmapped static country")
	}
}

// --- NewGeoResolver wiring (the config semantics) ---

func TestNewGeoResolverOfflineMode(t *testing.T) {
	// use_external_geo_ip_discovery=false + static_geo=RU → never any
	// network: the resolver is the static one.
	r := NewGeoResolver(false, "RU")
	loc, err := r.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.Source != "static" || loc.Language != "ru-RU" {
		t.Errorf("offline mode must use static resolver, got %+v", loc)
	}
}

func TestProviderIPWhoisRealFormat(t *testing.T) {
	// Regression (review M1 on PR #100): the REAL ipwhois.app response
	// captured live 2026-08-31 — "country" holds a FULL NAME, the ISO-2
	// code lives in "country_code", and timezone is a nested object.
	// The old parser failed on this 100% of the time.
	const payload = `{"ip":"95.105.4.122","success":true,"country":"Russian Federation","country_code":"RU","timezone":{"id":"Europe/Moscow","abbr":"MSK","is_dst":false,"offset":10800}}`
	p, _, _ := stubProvider(t, "ipwhois", payload, 0)
	loc, err := p.Resolve(context.Background(), &http.Client{}, "")
	if err != nil {
		t.Fatalf("ipwhois real format must parse: %v", err)
	}
	if loc.Country != "RU" || loc.Language != "ru-RU" || loc.Timezone != "Europe/Moscow" {
		t.Errorf("locale = %+v, want RU/ru-RU/Europe/Moscow", loc)
	}
}

func TestProviderFullCountryNameRejected(t *testing.T) {
	// A full name in "country" with NO ISO-2 field anywhere must error
	// (never feed "Russian Federation" to the mapping table).
	const payload = `{"country":"Russian Federation","timezone":"Europe/Moscow"}`
	p, _, _ := stubProvider(t, "stub", payload, 0)
	if _, err := p.Resolve(context.Background(), &http.Client{}, ""); err == nil {
		t.Error("expected error when only a full country name is present")
	}
}

func TestNewGeoResolverDiscoveryWithStaticFallback(t *testing.T) {
	// discovery=true + static_geo set → static wins only when the chain
	// (here: real providers, unreachable in test env without network...
	// but they may BE reachable). To keep this hermetic, assert the type
	// composition instead of the outcome.
	r := NewGeoResolver(true, "RU")
	if _, ok := r.(*fallbackResolver); !ok {
		t.Fatalf("expected fallbackResolver composition, got %T", r)
	}
	// And with no static_geo → bare chain.
	r2 := NewGeoResolver(true, "")
	if _, ok := r2.(*chainResolver); !ok {
		t.Fatalf("expected chainResolver, got %T", r2)
	}
}
