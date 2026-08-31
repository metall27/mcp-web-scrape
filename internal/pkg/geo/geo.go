// Package geo resolves the geography of the scraper's egress IP and maps it
// to a coherent browser locale/timezone (#99).
//
// Anti-bot systems flag the mismatch "German locale from a Kazan IP" as a
// VPN/bot signal instantly. The profile's language and timezone must agree
// with the geography of the IP the traffic actually leaves from:
//
//	direct egress  -> resolve the machine's public IP
//	proxy enabled  -> resolve THROUGH the proxy (its egress is what the site sees)
//
// Resolution is best-effort with a memory cache (TTL) and a static fallback
// table; a failed lookup must never break a scrape — it just degrades to the
// legacy random locale.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Locale is the geo-derived locale identity for a profile.
type Locale struct {
	// Country is the ISO 3166-1 alpha-2 code from the resolver ("RU").
	Country string
	// Timezone is the IANA zone of the egress IP ("Europe/Moscow").
	Timezone string
	// Language is the BCP-47 tag matching the country ("ru-RU").
	Language string
	// Source records where the geo came from: "resolver" | "cache" | "fallback".
	Source string
}

// Resolver looks up the geography of the current egress IP.
// The proxyURL parameter selects the egress: "" means direct.
type Resolver interface {
	Resolve(ctx context.Context, proxyURL string) (Locale, error)
}

// ipinfoResolver is the default Resolver: https://ipinfo.io/json (free,
// no token, returns country + timezone for the calling IP).
type ipinfoResolver struct {
	client *http.Client
	// endpoint allows tests to point the resolver at a stub server.
	endpoint string
}

// NewIPInfoResolver returns a Resolver backed by ipinfo.io.
func NewIPInfoResolver() Resolver {
	return &ipinfoResolver{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: "https://ipinfo.io/json",
	}
}

type ipinfoResponse struct {
	IP       string `json:"ip"`
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
}

func (r *ipinfoResolver) Resolve(ctx context.Context, proxyURL string) (Locale, error) {
	client := r.client
	if proxyURL != "" {
		pu, err := parseProxyURL(proxyURL)
		if err != nil {
			return Locale{}, fmt.Errorf("geo: invalid proxy URL: %w", err)
		}
		// Fresh transport only on the (rare) proxied path; the direct
		// path reuses the resolver's client and its connection pool.
		proxied := *r.client
		proxied.Transport = &http.Transport{Proxy: http.ProxyURL(pu)}
		client = &proxied
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return Locale{}, err
	}
	req.Header.Set("User-Agent", "curl/8.4.0") // honest UA — no browser pretense for a geo API
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return Locale{}, fmt.Errorf("geo: ipinfo request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Locale{}, fmt.Errorf("geo: ipinfo returned HTTP %d", resp.StatusCode)
	}

	var body ipinfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Locale{}, fmt.Errorf("geo: ipinfo decode failed: %w", err)
	}
	if body.Country == "" || body.Timezone == "" {
		return Locale{}, fmt.Errorf("geo: ipinfo response missing country/timezone (country=%q tz=%q)", body.Country, body.Timezone)
	}

	lang := LanguageForCountry(body.Country)
	if lang == "" {
		return Locale{}, fmt.Errorf("geo: no locale mapping for country %q", body.Country)
	}
	return Locale{Country: body.Country, Timezone: body.Timezone, Language: lang, Source: "resolver"}, nil
}
