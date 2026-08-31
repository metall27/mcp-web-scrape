package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Provider is a single geo-IP lookup endpoint. All built-in providers are
// interchangeable JSON services that locate the CALLING IP (no query
// parameter), so a chain of them can be tried in order until one answers.
type Provider interface {
	Name() string
	Resolve(ctx context.Context, client *http.Client, proxyURL string) (Locale, error)
}

// jsonProvider is a generic {"country": "...", "timezone": "..."} provider.
type jsonProvider struct {
	name     string
	endpoint string
}

// Built-in provider chain, tried in order. All are free, keyless, and
// locate the calling IP. The chain means NO single external resource can
// disable the feature (#99 review follow-up: no hard dependency on one
// service). Order: ipinfo (most precise fields) → ip-api → ipwhois.
var builtinProviders = []Provider{
	&jsonProvider{name: "ipinfo", endpoint: "https://ipinfo.io/json"},
	&jsonProvider{name: "ip-api", endpoint: "http://ip-api.com/json/?fields=status,countryCode,timezone"},
	&jsonProvider{name: "ipwhois", endpoint: "https://ipwhois.app/json"},
}

// BuiltinProviders returns a copy of the default provider chain (used when
// the config lists none).
func BuiltinProviders() []Provider {
	out := make([]Provider, len(builtinProviders))
	copy(out, builtinProviders)
	return out
}

// providerResponse is the superset of fields across the built-in providers.
type providerResponse struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`     // ipinfo, ipwhois
	CountryCode string `json:"countryCode"` // ip-api
	Timezone    string `json:"timezone"`    // all
	Status      string `json:"status"`      // ip-api ("success"), ipwhois ("success")
	Message     string `json:"message"`     // ipwhois error text
}

func (p *jsonProvider) Name() string { return p.name }

func (p *jsonProvider) Resolve(ctx context.Context, client *http.Client, proxyURL string) (Locale, error) {
	httpClient := client
	if proxyURL != "" {
		pu, err := parseProxyURL(proxyURL)
		if err != nil {
			return Locale{}, fmt.Errorf("geo[%s]: invalid proxy URL: %w", p.name, err)
		}
		// Fresh transport only on the (currently inactive) proxied path;
		// the direct path reuses the shared client's connection pool.
		proxied := *client
		proxied.Transport = &http.Transport{Proxy: http.ProxyURL(pu)}
		httpClient = &proxied
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return Locale{}, err
	}
	// Honest UA — a geo API is not something to pretend a browser at.
	req.Header.Set("User-Agent", "curl/8.4.0")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Locale{}, fmt.Errorf("geo[%s]: request failed: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Locale{}, fmt.Errorf("geo[%s]: HTTP %d", p.name, resp.StatusCode)
	}

	var body providerResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Locale{}, fmt.Errorf("geo[%s]: decode failed: %w", p.name, err)
	}

	// ip-api / ipwhois signal failures inside a 200 via status != "success".
	if body.Status != "" && body.Status != "success" {
		return Locale{}, fmt.Errorf("geo[%s]: provider status %q %q", p.name, body.Status, body.Message)
	}

	// Providers disagree on the field name (country vs countryCode).
	country := body.Country
	if country == "" {
		country = body.CountryCode
	}
	if country == "" || body.Timezone == "" {
		return Locale{}, fmt.Errorf("geo[%s]: missing country/timezone (country=%q tz=%q)", p.name, country, body.Timezone)
	}

	lang := LanguageForCountry(country)
	if lang == "" {
		return Locale{}, fmt.Errorf("geo[%s]: no locale mapping for country %q", p.name, country)
	}
	return Locale{Country: country, Timezone: body.Timezone, Language: lang, Source: p.name}, nil
}

// chainResolver tries providers in order and returns the first success.
// It replaces the old single-service resolver: one dead service no longer
// disables the feature.
type chainResolver struct {
	providers []Provider
	client    *http.Client
}

// NewChainResolver builds a Resolver over an ordered provider list. An
// empty list falls back to BuiltinProviders().
func NewChainResolver(providers []Provider) Resolver {
	if len(providers) == 0 {
		providers = BuiltinProviders()
	}
	return &chainResolver{
		providers: providers,
		client:    &http.Client{Timeout: 4 * time.Second},
	}
}

// staticResolver serves a config-pinned country with zero network.
type staticResolver struct {
	locale Locale
}

// NewStaticResolver returns a Resolver that always answers with the
// locale mapped from the given ISO-2 country ("RU" → ru-RU/Europe/Moscow).
// An unmapped/empty country yields a resolver that always errors (the
// caller treats it as "not configured").
func NewStaticResolver(country string) Resolver {
	l, ok := localeForCountry(country)
	if !ok {
		return &staticResolver{locale: Locale{}}
	}
	l.Source = "static"
	return &staticResolver{locale: l}
}

func (s *staticResolver) Resolve(ctx context.Context, proxyURL string) (Locale, error) {
	if s.locale.Country == "" {
		return Locale{}, fmt.Errorf("geo[static]: country not configured or unmapped")
	}
	return s.locale, nil
}

// NewGeoResolver builds the production resolver per config (#99):
//
//	useExternalDiscovery=false → staticResolver(staticGeo), the network
//	  is never touched;
//	useExternalDiscovery=true  → provider chain (CachedResolver at the
//	  caller); a non-empty staticGeo becomes a fallback for the
//	  "all providers dead" case.
func NewGeoResolver(useExternalDiscovery bool, staticGeo string) Resolver {
	static := NewStaticResolver(staticGeo)
	if !useExternalDiscovery {
		return static
	}
	if _, err := static.Resolve(context.Background(), ""); err == nil {
		return &fallbackResolver{primary: NewChainResolver(nil), fallback: static}
	}
	return NewChainResolver(nil)
}

// fallbackResolver tries primary, then fallback.
type fallbackResolver struct {
	primary  Resolver
	fallback Resolver
}

func (f *fallbackResolver) Resolve(ctx context.Context, proxyURL string) (Locale, error) {
	l, err := f.primary.Resolve(ctx, proxyURL)
	if err == nil {
		return l, nil
	}
	if fl, ferr := f.fallback.Resolve(ctx, proxyURL); ferr == nil {
		return fl, nil
	}
	return Locale{}, err
}

func (c *chainResolver) Resolve(ctx context.Context, proxyURL string) (Locale, error) {
	var lastErr error
	for _, p := range c.providers {
		loc, err := p.Resolve(ctx, c.client, proxyURL)
		if err == nil {
			return loc, nil
		}
		lastErr = err
	}
	return Locale{}, fmt.Errorf("geo: all %d providers failed: %w", len(c.providers), lastErr)
}
