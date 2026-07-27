package tools

import (
	"net/url"
	"strings"
	"testing"
)

// TestRedactURL covers the credential-redaction helper added in issue #69.
// Sensitive query params must be masked in logs/metadata; everything else
// (scheme/host/path/safe params/empty/invalid inputs) must pass through intact.
//
// We compare via re-parsing the query (not raw string equality) because
// url.Values iteration order is non-deterministic and our manual query builder
// preserves insertion order of the input but still varies across map iteration.
func TestRedactURL(t *testing.T) {
	// assertQueryParam is a helper: parses got as a URL, checks that every
	// sensitive key is masked and every safe key keeps its value.
	assertRedacted := func(t *testing.T, got, originalNonQueryPart string, expect map[string]string) {
		t.Helper()
		if !strings.HasPrefix(got, originalNonQueryPart) {
			t.Errorf("non-query prefix changed:\n  got:  %q\n  want: %q", got, originalNonQueryPart)
			return
		}
		// Everything after the first '?' is the query string.
		qIdx := strings.Index(got, "?")
		var rawQ string
		if qIdx >= 0 {
			rawQ = got[qIdx+1:]
		}
		if rawQ == "" {
			t.Errorf("query part is empty in %q", got)
			return
		}
		gotVals, err := url.ParseQuery(rawQ)
		if err != nil {
			t.Errorf("redacted result has unparseable query: %v", err)
			return
		}
		for k, want := range expect {
			if actual := gotVals.Get(k); actual != want {
				t.Errorf("key %q = %q, want %q", k, actual, want)
			}
		}
	}

	t.Run("rebrainme login+password GET leak masked", func(t *testing.T) {
		got := RedactURL("https://my.rebrainme.com/?login=dpavlov85@yandex.ru&password=Y2RkNDkx")
		assertRedacted(t, got, "https://my.rebrainme.com", map[string]string{
			"login":    "***",
			"password": "***",
		})
	})

	t.Run("only password masked, safe params preserved", func(t *testing.T) {
		got := RedactURL("https://example.com/api?id=42&password=secret&debug=1")
		assertRedacted(t, got, "https://example.com/api", map[string]string{
			"id":       "42",
			"password": "***",
			"debug":    "1",
		})
	})

	t.Run("no query string passes through unchanged", func(t *testing.T) {
		in := "https://example.com/path"
		if got := RedactURL(in); got != in {
			t.Errorf("RedactURL(%q) = %q, want unchanged", in, got)
		}
	})

	t.Run("safe query params all preserved", func(t *testing.T) {
		got := RedactURL("https://example.com/search?q=golang&page=2")
		assertRedacted(t, got, "https://example.com/search", map[string]string{
			"q":    "golang",
			"page": "2",
		})
	})

	t.Run("case-insensitive key match", func(t *testing.T) {
		got := RedactURL("https://example.com/?PASSWORD=hunter2&TOKEN=abc")
		// Preserve the original case of keys; only values are masked.
		assertRedacted(t, got, "https://example.com", map[string]string{
			"PASSWORD": "***",
			"TOKEN":    "***",
		})
	})

	t.Run("api_key masked, unrelated key preserved", func(t *testing.T) {
		got := RedactURL("https://api.example.com/v1?key=12345&api_key=secret_key")
		assertRedacted(t, got, "https://api.example.com/v1", map[string]string{
			"key":     "12345",
			"api_key": "***",
		})
	})

	t.Run("empty string returns empty", func(t *testing.T) {
		if got := RedactURL(""); got != "" {
			t.Errorf("RedactURL(\"\") = %q, want \"\"", got)
		}
	})

	t.Run("non-URL string returned as-is (best-effort, no panic)", func(t *testing.T) {
		in := "not a url at all"
		if got := RedactURL(in); got != in {
			t.Errorf("RedactURL(%q) = %q, want as-is", in, got)
		}
	})

	t.Run("does not URL-encode the placeholder", func(t *testing.T) {
		// Regression: url.Values.Encode() turns "***" into "%2A%2A%2A".
		got := RedactURL("https://x.com/?password=secret")
		if strings.Contains(got, "%2A") {
			t.Errorf("placeholder got URL-encoded in result: %q", got)
		}
		if !strings.Contains(got, "password=***") {
			t.Errorf("expected literal password=*** in %q", got)
		}
	})
}
