package geo

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// countryLocales maps ISO 3166-1 alpha-2 country codes to a coherent
// BCP-47 language + IANA timezone pair. The table covers the countries the
// scraper realistically egresses from (RU home/bare-metal, common proxy
// locations); unmapped countries fall back to the legacy random locale.
var countryLocales = map[string]struct{ Language, Timezone string }{
	"RU": {"ru-RU", "Europe/Moscow"},
	"BY": {"ru-RU", "Europe/Minsk"},
	"KZ": {"ru-RU", "Asia/Almaty"},
	"UA": {"uk-UA", "Europe/Kyiv"},
	"DE": {"de-DE", "Europe/Berlin"},
	"FR": {"fr-FR", "Europe/Paris"},
	"ES": {"es-ES", "Europe/Madrid"},
	"IT": {"it-IT", "Europe/Rome"},
	"PL": {"pl-PL", "Europe/Warsaw"},
	"NL": {"nl-NL", "Europe/Amsterdam"},
	"GB": {"en-GB", "Europe/London"},
	"US": {"en-US", "America/New_York"},
	"CA": {"en-CA", "America/Toronto"},
	"TR": {"tr-TR", "Europe/Istanbul"},
	"IL": {"he-IL", "Asia/Jerusalem"},
	"AE": {"ar-AE", "Asia/Dubai"},
	"CN": {"zh-CN", "Asia/Shanghai"},
	"JP": {"ja-JP", "Asia/Tokyo"},
	"KR": {"ko-KR", "Asia/Seoul"},
	"IN": {"en-IN", "Asia/Kolkata"},
	"BR": {"pt-BR", "America/Sao_Paulo"},
	"AU": {"en-AU", "Australia/Sydney"},
}

// LanguageForCountry returns the BCP-47 language for an ISO country code,
// or "" when the country is not in the table.
func LanguageForCountry(country string) string {
	if l, ok := countryLocales[normalizeCountry(country)]; ok {
		return l.Language
	}
	return ""
}

// LocaleForCountry returns the full static locale for an ISO country code
// (used by the resolver fallback when the network lookup fails but the
// country is known, e.g. from config).
func LocaleForCountry(country string) (Locale, bool) {
	l, ok := countryLocales[normalizeCountry(country)]
	if !ok {
		return Locale{}, false
	}
	return Locale{Country: normalizeCountry(country), Timezone: l.Timezone, Language: l.Language, Source: "fallback"}, true
}

func normalizeCountry(c string) string {
	if len(c) != 2 {
		return c
	}
	// uppercase in-place equivalent without pulling strings for clarity
	if c[0] >= 'a' && c[0] <= 'z' || c[1] >= 'a' && c[1] <= 'z' {
		return string([]byte{up(c[0]), up(c[1])})
	}
	return c
}

func up(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}

// parseProxyURL normalizes a proxy URL for http.ProxyURL (adds http:// when
// the scheme is missing, mirroring the proxy rotator's parseProxy).
func parseProxyURL(proxyURL string) (*url.URL, error) {
	if proxyURL == "" {
		return nil, nil
	}
	u := proxyURL
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	return url.Parse(u)
}

// CachedResolver wraps a Resolver with an in-memory TTL cache keyed by the
// egress (proxy URL, or "" for direct). The egress IP of a residential
// connection or a sticky proxy changes rarely; resolving on every scrape
// both wastes the ipinfo quota and adds latency.
type CachedResolver struct {
	inner Resolver
	ttl   time.Duration

	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	locale  Locale
	expires time.Time
}

// NewCachedResolver wraps r with a cache of the given TTL
// (10 minutes is the recommended default).
func NewCachedResolver(r Resolver, ttl time.Duration) *CachedResolver {
	return &CachedResolver{inner: r, ttl: ttl, entries: map[string]cacheEntry{}}
}

// Resolve returns the cached locale when fresh, otherwise delegates to the
// inner resolver and caches the result. Errors are NOT cached — a transient
// network failure should not pin a wrong/fallback locale for the TTL.
func (c *CachedResolver) Resolve(ctx context.Context, proxyURL string) (Locale, error) {
	key := proxyURL // "" = direct egress

	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expires) {
		c.mu.Unlock()
		l := e.locale
		l.Source = "cache"
		return l, nil
	}
	c.mu.Unlock()

	l, err := c.inner.Resolve(ctx, proxyURL)
	if err != nil {
		return Locale{}, err
	}

	c.mu.Lock()
	c.entries[key] = cacheEntry{locale: l, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return l, nil
}
