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

// --- ipinfoResolver against a stub server (no network) ---

func TestIPInfoResolverParses(t *testing.T) {
	const payload = `{"ip":"95.105.4.122","city":"Kazan","country":"RU","timezone":"Europe/Moscow"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, payload)
	}))
	defer srv.Close()

	res := &ipinfoResolver{client: srv.Client(), endpoint: srv.URL + "/json"}
	loc, err := res.Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Country != "RU" || loc.Timezone != "Europe/Moscow" || loc.Language != "ru-RU" {
		t.Errorf("locale = %+v, want RU/Europe/Moscow/ru-RU", loc)
	}
	if loc.Source != "resolver" {
		t.Errorf("source = %q, want resolver", loc.Source)
	}
}

func TestIPInfoResolverUnknownCountry(t *testing.T) {
	const payload = `{"ip":"1.2.3.4","country":"XX","timezone":"Nowhere/Foo"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, payload)
	}))
	defer srv.Close()

	res := &ipinfoResolver{client: srv.Client(), endpoint: srv.URL + "/json"}
	if _, err := res.Resolve(context.Background(), ""); err == nil {
		t.Error("expected error for unmapped country")
	}
}

func TestIPInfoResolverHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	res := &ipinfoResolver{client: srv.Client(), endpoint: srv.URL + "/json"}
	if _, err := res.Resolve(context.Background(), ""); err == nil {
		t.Error("expected error on HTTP 429")
	}
}
