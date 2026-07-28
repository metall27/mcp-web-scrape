package browser

import (
	"context"

	"github.com/chromedp/chromedp"
)

// DOMSignals holds read-only page-classification hints extracted from the DOM
// after the page has settled. These are PASSIVE observations surfaced in scrape
// metadata to help the LLM decide how to proceed — they never mutate page state
// or alter the scrape flow. This is the classify_page enrichment from #77.
//
// Design principle (#77 consensus): heuristics are hints in metadata, not
// commands. "login form detected" does not mean the tool auto-fills; it means
// the LLM sees the signal and can choose to supply credentials.
type DOMSignals struct {
	// HasLoginForm is true when the page contains an input[type=password],
	// the canonical "this page wants credentials" signal. Combined with a
	// 401/403 in NetworkSummary this is a strong auth-required indicator.
	HasLoginForm bool `json:"has_login_form,omitempty"`

	// IsSPA is true when the page shows client-side-rendering markers (a root
	// mount node with near-empty body, or a known framework's hydration
	// globals). Signals the LLM that settle/network-idle matters, not instant
	// snapshot.
	IsSPA bool `json:"is_spa,omitempty"`

	// Framework names the detected JS framework, if any: "react", "vue",
	// "angular", "svelte", "next", "nuxt". Empty when not detected. This is a
	// reference hint, not a directive — it helps the LLM reason about the page
	// but does not change tool behavior.
	Framework string `json:"framework,omitempty"`

	// BlockedHint flags a likely anti-bot interstitial: "cloudflare",
	// "recaptcha", "hcaptcha", or "turnstile". Conservative heuristic — the
	// NetworkSummary.BlockedByCDN (429/503 response codes) is the primary
	// signal; this DOM-side check catches challenge pages that load with HTTP
	// 200 but no real content.
	BlockedHint string `json:"blocked_hint,omitempty"`
}

// classifyPageJS is the DOM probe. It runs as a single Evaluate round-trip to
// keep overhead minimal. All checks are defensive (guarded against missing
// nodes) so a partially-loaded page degrades to empty signals, not an error.
const classifyPageJS = `(() => {
	// Login form: password input is the strongest single signal.
	let hasLogin = false;
	try { hasLogin = !!document.querySelector('input[type="password"]'); } catch(e) {}

	// SPA / framework detection. Order matters for accuracy: check for
	// meta-frameworks (Next/Nuxt) first since they build on React/Vue.
	let isSPA = false;
	let framework = '';

	// Next.js
	if (document.getElementById('__next') || document.getElementById('__NEXT_DATA__')) {
		isSPA = true; framework = 'next';
	} else if (document.getElementById('__nuxt') || document.getElementById('__NUXT_DATA__')) {
		isSPA = true; framework = 'nuxt';
	} else if (document.querySelector('[data-reactroot],[data-reactroot-id]') ||
		typeof window.__REACT_DEVTOOLS_GLOBAL_HOOK__ !== 'undefined' ||
		(document.body && document.body._reactRootContainer !== undefined)) {
		isSPA = true; framework = 'react';
	} else if (document.querySelector('[data-v-app],.nuxt-app') ||
		document.querySelector('[data-v-]')) {
		isSPA = true; framework = 'vue';
	} else if (document.querySelector('app-root,[ng-version]')) {
		isSPA = true; framework = 'angular';
	} else {
		// Generic CSR heuristic: a root mount div with an otherwise near-empty
		// body — the classic pre-hydration shell.
		const root = document.getElementById('root') || document.getElementById('app');
		if (root && document.body && (document.body.innerText || '').trim().length < 100) {
			isSPA = true;
		}
	}

	// Anti-bot / captcha interstitials (DOM-side; conservative).
	let blockedHint = '';
	try {
		if (document.getElementById('cf-challenge-running') ||
			document.getElementById('cf-challenge') ||
			document.querySelector('[class*="cf-mitigated"]') ||
			document.title.toLowerCase().includes('just a moment')) {
			blockedHint = 'cloudflare';
		} else if (document.querySelector('.g-recaptcha,iframe[src*="recaptcha"]')) {
			blockedHint = 'recaptcha';
		} else if (document.querySelector('.h-captcha,iframe[src*="hcaptcha"]')) {
			blockedHint = 'hcaptcha';
		} else if (document.querySelector('.cf-turnstile')) {
			blockedHint = 'turnstile';
		}
	} catch(e) {}

	return { has_login_form: hasLogin, is_spa: isSPA, framework, blocked_hint: blockedHint };
})()`

// ClassifyPage runs the read-only DOM probe and returns the extracted signals.
// It must be called inside a chromedp.Run/ActionFunc with an active CDP context
// (the page loaded). Returns a zero-value DOMSignals on Evaluate failure —
// classify_page is non-fatal enrichment; it never aborts the scrape.
func ClassifyPage(ctx context.Context) DOMSignals {
	var probe struct {
		HasLoginForm bool   `json:"has_login_form"`
		IsSPA        bool   `json:"is_spa"`
		Framework    string `json:"framework"`
		BlockedHint  string `json:"blocked_hint"`
	}
	// Non-fatal: a failed Evaluate (page navigating, detached frame) leaves
	// zero signals rather than poisoning the whole scrape.
	if err := chromedp.Evaluate(classifyPageJS, &probe).Do(ctx); err != nil {
		return DOMSignals{}
	}
	return DOMSignals{
		HasLoginForm: probe.HasLoginForm,
		IsSPA:        probe.IsSPA,
		Framework:    probe.Framework,
		BlockedHint:  probe.BlockedHint,
	}
}
