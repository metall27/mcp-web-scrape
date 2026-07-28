package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog"
)

type ScrapeJSTool struct {
	*BaseTool
	scraper   Scraper
	cache     *cache.Cache
	ragConfig config.RAGConfig
	githubCfg config.GitHubConfig
	logger    zerolog.Logger
}

// buildActionsSchema returns the JSON-schema "actions" array property for
// scrape_with_js. Each action type is a separate oneOf branch whose required
// fields match what ParseActions / validateAction enforce in actions.go, so
// the schema itself encodes "wait_for needs selector", "wait_for_text needs
// text", etc. — instead of a flat property bag where the LLM has to guess
// which fields each type requires.
//
// Without this, agents repeatedly send {type:"wait_for"} with no selector
// (confusing the top-level wait_for string param with the action type),
// producing "selector is required for wait_for action" errors.
func buildActionsSchema() map[string]interface{} {
	timeoutProp := func() map[string]interface{} {
		return map[string]interface{}{
			"type":        "integer",
			"description": "Timeout in milliseconds for this action (default: 30000)",
		}
	}
	retriesProp := func() map[string]interface{} {
		return map[string]interface{}{
			"type":        "integer",
			"description": "Number of retries on failure (default: 3)",
		}
	}

	// selectorOnly: oneOf branch for actions needing {selector}.
	selectorOnly := func(actionType, selDesc string) map[string]interface{} {
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{actionType},
					"description": "Action type",
				},
				"selector": map[string]interface{}{
					"type":        "string",
					"description": selDesc,
				},
				"timeout": timeoutProp(),
				"retries": retriesProp(),
			},
			"required":             []string{"type", "selector"},
			"additionalProperties": false,
		}
	}

	// selectorAndText: oneOf branch for actions needing {selector, text}.
	selectorAndText := func(actionType, selDesc, textDesc string) map[string]interface{} {
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{actionType},
					"description": "Action type",
				},
				"selector": map[string]interface{}{
					"type":        "string",
					"description": selDesc,
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": textDesc,
				},
				"timeout": timeoutProp(),
				"retries": retriesProp(),
			},
			"required":             []string{"type", "selector", "text"},
			"additionalProperties": false,
		}
	}

	// textOnly: oneOf branch for actions needing {text} with no selector.
	textOnly := func(actionType, textDesc string) map[string]interface{} {
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{actionType},
					"description": "Action type",
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": textDesc,
				},
				"timeout": timeoutProp(),
				"retries": retriesProp(),
			},
			"required":             []string{"type", "text"},
			"additionalProperties": false,
		}
	}

	// noFields: oneOf branch for actions needing only {type} (+ optional
	// timeout/retries). Used by wait_for_navigation and wait_for_content —
	// escape-hatch wait actions introduced for SPA flows (issue #71).
	noFields := func(actionType, desc string) map[string]interface{} {
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{actionType},
					"description": "Action type",
				},
				"timeout": timeoutProp(),
				"retries": retriesProp(),
			},
			"required":             []string{"type"},
			"additionalProperties": false,
			"description":          desc,
		}
	}

	return map[string]interface{}{
		"type": "array",
		"description": "Ordered list of interactive actions to run after page load (not cached). " +
			"Each action is an object whose 'type' field determines which other fields are required. " +
			"Match the oneOf branch for the type you are using — omitting a required field is a hard error.",
		"items": map[string]interface{}{
			"oneOf": []interface{}{
				// --- selector-only actions ---
				selectorOnly("click", "CSS selector for the element to click"),
				selectorOnly("submit", "CSS selector for the form or submit button"),
				selectorOnly("scroll_to", "CSS selector for the element to scroll into view"),
				selectorOnly("wait_for", "CSS selector for the element that must become visible before continuing"),
				selectorOnly("hover", "CSS selector for the element to hover over"),
				// --- selector + text actions ---
				selectorAndText("type", "CSS selector for the input field", "Text to type into the field"),
				selectorAndText("upload_file", "CSS selector for the file input element", "Path to the file to upload"),
				// --- selector + value action (select_option) ---
				map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"select_option"},
							"description": "Action type",
						},
						"selector": map[string]interface{}{
							"type":        "string",
							"description": "CSS selector for the <select> dropdown element",
						},
						"value": map[string]interface{}{
							"type":        "string",
							"description": "Value attribute of the <option> to select",
						},
						"timeout": timeoutProp(),
						"retries": retriesProp(),
					},
					"required":             []string{"type", "selector", "value"},
					"additionalProperties": false,
				},
				// --- text-only actions ---
				textOnly("execute_js", "JavaScript code to execute. Do NOT use top-level 'return' — wrap in an IIFE: (() => { ... })(). The return value IS included in metadata.execute_js_results."),
				textOnly("wait_for_text", "Text to wait for on the page (polls until document.body.innerText includes this string). Note: a timeout here is NON-FATAL (soft) — the scrape continues and the current page is still returned, so you can inspect the result and adapt. Prefer observe_changes=true for login flows instead of guessing exact text to wait for."),
				textOnly("navigate", "URL to navigate to"),
				// --- field-less wait actions (escape hatch for SPA flows, #71) ---
				noFields("wait_for_navigation", "Wait until the page URL changes from its value at the moment this action starts (detects both server redirects and client-side SPA routing via the history API). Useful after click/submit to confirm a navigation actually happened. A timeout is NON-FATAL (soft) — the scrape continues. Note: a client-side route change may update the URL before the DOM re-renders; follow with wait_for_content if you need the rendered content."),
				noFields("wait_for_content", "Wait until document.body has non-trivial content AND its hash stops changing (the page has settled). Solves the empty-page-after-navigate problem on SPAs: after navigate the page loads as an empty shell and React/Vue/Zustand render content asynchronously. A timeout is NON-FATAL (soft)."),
				// --- login composite action (#77 Tier-2) ---
				map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"login"},
							"description": "Composite login action (#77 Tier-2): fills username + password fields, submits, and verifies the outcome. Returns a structured login_result in metadata with a verdict (success/ambiguous/auth_failed) and evidence (URL changed, cookies grew, auth keys appeared) — so you see WHY auth worked or failed, not just success/error. Prefer this over a manual type→type→click chain for login flows. The submit selector is optional: if omitted, the tool auto-detects button[type=submit] nearest the password field. Credentials are used only for this action; pair with session_id to persist the authenticated session across calls.",
						},
						"username_selector": map[string]interface{}{
							"type":        "string",
							"description": "CSS selector for the username/email input field",
						},
						"username": map[string]interface{}{
							"type":        "string",
							"description": "Username or email credential value",
						},
						"password_selector": map[string]interface{}{
							"type":        "string",
							"description": "CSS selector for the password input field (input[type=password])",
						},
						"password": map[string]interface{}{
							"type":        "string",
							"description": "Password credential value",
						},
						"submit_selector": map[string]interface{}{
							"type":        "string",
							"description": "CSS selector for the submit button (optional — auto-detected as button[type=submit] nearest the password field when omitted)",
						},
						"timeout": timeoutProp(),
						"retries": retriesProp(),
					},
					"required":             []string{"type", "username_selector", "username", "password_selector", "password"},
					"additionalProperties": false,
				},
			},
		},
	}
}

func NewScrapeJSTool(cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, browserCfg config.BrowserConfig, uaRotator *useragent.Rotator, proxyRotator *proxy.Rotator, githubCfg config.GitHubConfig) *ScrapeJSTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"format":      "uri",
				"description": "The URL to scrape with JavaScript rendering",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Page load timeout in seconds (default: 60)",
				"default":     60,
			},
			"wait_for": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector to wait for before scraping (optional)",
			},
			"wait_time": map[string]interface{}{
				"type":        "integer",
				"description": "Additional wait time in milliseconds after page load (default: 3000)",
				"default":     3000,
			},
			"screenshot": map[string]interface{}{
				"type":        "boolean",
				"description": "Take a screenshot of the page (base64 encoded)",
				"default":     false,
			},
			"screenshot_mode": map[string]interface{}{
				"type":        "string",
				"description": "When to take screenshot: never, auto (default - if HTML > 50KB), always",
				"enum":        []string{"never", "auto", "always"},
				"default":     "auto",
			},
			"user_agent": map[string]interface{}{
				"type":        "string",
				"description": "Custom user agent string",
			},
			"viewport_width": map[string]interface{}{
				"type":        "integer",
				"description": "Browser viewport width in pixels (default: 1920)",
				"default":     1920,
			},
			"viewport_height": map[string]interface{}{
				"type":        "integer",
				"description": "Browser viewport height in pixels (default: 1080)",
				"default":     1080,
			},
			"block_images": map[string]interface{}{
				"type":        "boolean",
				"description": "Block images from loading (faster scraping)",
				"default":     false,
			},
			"wait_for_network_idle": map[string]interface{}{
				"type":        "boolean",
				"description": "Wait for network idle instead of fixed wait_time (smarter, 30s timeout)",
				"default":     false,
			},
			"output_format": map[string]interface{}{
				"type":        "string",
				"description": "Output format: markdown (default, 75% smaller, better for LLMs) or html (raw HTML)",
				"enum":        []string{"html", "markdown"},
				"default":     "markdown",
			},
			"stealth_enabled": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable stealth mode: injects anti-detection scripts (hides navigator.webdriver, spoofs canvas/WebGL/audio fingerprint, randomizes hardwareConcurrency/timezone/platform) that persist across page navigations. Use for sites with anti-bot measures that conditionally hide elements (e.g. login buttons) from automated browsers.",
				"default":     false,
			},
			"stealth_scroll": map[string]interface{}{
				"type":        "boolean",
				"description": "Emulate scrolling behavior (many SPAs load content on scroll)",
				"default":     true,
			},
			"stealth_mouse": map[string]interface{}{
				"type":        "boolean",
				"description": "Emulate random mouse movements (advanced anti-bot evasion)",
				"default":     false,
			},
			"session_id": map[string]interface{}{
				"type":        "string",
				"description": "Named persistent browser session. When set, the browser context (cookies, localStorage, sessionStorage) is reused across scrape_with_js calls with the same session_id — login once, then fetch N pages without re-authenticating. Sessions auto-close after inactivity (configurable TTL, default 30m). If empty, each call is ephemeral (cookies cleared before navigation). Example: {\"session_id\":\"rebrain\"} on first call logs in; subsequent calls with the same session_id reuse the authenticated state.",
				"default":     "",
			},
			"close_session": map[string]interface{}{
				"type":        "boolean",
				"description": "Close the named session after this call (explicit cleanup). Only meaningful with session_id. Releases the browser context immediately instead of waiting for the inactivity TTL. Example: scrape_with_js(session_id=\"rebrain\", close_session=true) to free resources after a workflow completes.",
				"default":     false,
			},
			"observe_changes": map[string]interface{}{
				"type":        "boolean",
				"description": "After each mutating action (click/submit/type/select/navigate), capture a compact page snapshot (URL, title, top headings, visible error messages, body-changed flag, ~300-char text preview) and return it in metadata.action_observations. Lets you judge whether an action had its intended effect — login succeeded, content loaded, error appeared — WITHOUT guessing exact text to wait_for. Off by default (zero overhead on static scrapes). Recommended for login flows and multi-step interactive workflows. See issue #72.",
				"default":     false,
			},
			"smart_settle": map[string]interface{}{
				"type":        "boolean",
				"description": "After each mutating action (click/submit/type/navigate/...), wait for the page to settle — DOM content emerges and stops changing (~5s cap) — before running the next action and before capturing an observation. This mirrors how a real browser behaves: an async auth fetch or SPA route change takes time, and snapshotting the instant a click returns captures an empty or stale state (the cause of empty content after navigate on SPA sites). ON by default when actions are present; set false only if you need instant post-action snapshots. Auth signals (cookie count + auth-like localStorage key names) are included in each observation so you can see whether a login actually took effect. See issue #71.",
				"default":     true,
			},
			"actions": buildActionsSchema(),
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &ScrapeJSTool{
			cache:     cache,
			ragConfig: ragConfig,
			githubCfg: githubCfg,
			logger:    logger.Get(),
		}
		// ChromeScraper has its own retry loop and HTTP fallback
		// No need for RetryScraper wrapper
		tool.scraper = NewChromeScraper(cache, browserPool, ragConfig, browserCfg, uaRotator, proxyRotator, githubCfg)
		return tool.Execute(ctx, args)
	}

	tool := &ScrapeJSTool{
		cache:     cache,
		ragConfig: ragConfig,
		githubCfg: githubCfg,
		logger:    logger.Get(),
	}
	// ChromeScraper has its own retry loop and HTTP fallback
	// No need for RetryScraper wrapper
	tool.scraper = NewChromeScraper(cache, browserPool, ragConfig, browserCfg, uaRotator, proxyRotator, githubCfg)

	tool.BaseTool = NewBaseTool(
		"scrape_with_js",
		"Scrape a URL with full JavaScript rendering (headless Chrome). Use for dynamic sites: SPAs, dashboards, interactive pages, or any site that requires JS. For static pages (blogs, news, docs), prefer scrape_url (faster).\n\nReturns the page content as Markdown (default, ~75% smaller than HTML) or raw HTML. Optional screenshot capture. Interactive actions (click, type, scroll, wait, navigate) supported for login-protected or dynamically-loaded content.\n\nSmart feedback loop for interactive actions (issue #71): by default, when actions are present the scraper waits for the page to settle (DOM content emerges and stops changing, ~5s cap) after each mutating action before running the next one — no need to guess how long an async auth fetch or SPA route change takes. Set observe_changes=true to also receive compact page snapshots after each action in metadata.action_observations: URL, url_changed, title, headings, visible error messages, body_changed, auth signals (cookie count + auth-like localStorage key names), settle report, and a text preview. The auth signals let you see whether a login actually took effect even when the URL hasn't changed yet.\n\nAll wait-type actions (wait_for, wait_for_text, wait_for_navigation, wait_for_content) have NON-FATAL timeouts: the scrape continues and returns the current page state, so you can inspect what happened instead of guessing. For SPA login flows, prefer: type credentials -> click/submit -> (smart_settle handles the wait automatically) -> optionally wait_for_navigation + wait_for_content if you need explicit control.\n\nStealth mode (stealth_enabled=true) injects anti-detection scripts (hides navigator.webdriver, spoofs fingerprint) that persist across navigations — use for sites that conditionally hide elements (e.g. login buttons) from automated browsers.\n\nImportant execute_js notes: code runs via chromedp.Evaluate and does NOT support top-level 'return' — wrap code in an IIFE: (() => { ... })() or (function(){ ... })(). The return value IS included in metadata.execute_js_results of the response.\n\nAutomatic retry with exponential backoff on timeout/empty responses. Detects blocking (Cloudflare, captcha) and returns diagnostic hints. RAG auto-indexing applies only when RAG is enabled in server config.",
		schema,
		handler,
	)

	return tool
}

func (t *ScrapeJSTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Extract URL
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, fmt.Errorf("url is required and must be a string")
	}

	// Parse interactive actions if provided
	var interactiveActions []browser.Action
	hasActions := false
	if actionsData, ok := args["actions"].([]interface{}); ok && len(actionsData) > 0 {
		parsedActions, err := browser.ParseActions(actionsData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse actions: %w", err)
		}
		interactiveActions = parsedActions
		hasActions = true
		t.logger.Info().
			Int("actions_count", len(interactiveActions)).
			Msg("Interactive actions detected")
	}

	// Build options from args
	opts := t.buildOptions(args, interactiveActions)

	// Log request
	t.logger.Info().
		Str("url", urlStr).
		Int("timeout", int(opts.Timeout.Seconds())).
		Str("wait_for", opts.WaitForSelector).
		Int("wait_time_ms", int(opts.WaitForDuration.Milliseconds())).
		Bool("wait_for_network_idle", opts.WaitForNetworkIdle).
		Bool("stealth_enabled", opts.StealthEnabled).
		Bool("stealth_scroll", opts.StealthScroll).
		Bool("stealth_mouse", opts.StealthMouse).
		Str("output_format", opts.OutputFormat).
		Bool("screenshot", opts.Screenshot).
		Str("screenshot_mode", opts.ScreenshotMode).
		Str("session_id", opts.SessionID).
		Bool("close_session", opts.CloseSession).
		Msg("Starting JavaScript-rendered scrape")

	// Validate scraper is initialized
	if t.scraper == nil {
		return nil, fmt.Errorf("Chrome scraper is not initialized - this is a configuration error")
	}

	// Execute scrape using ChromeScraper
	result, err := t.scraper.Scrape(ctx, urlStr, opts)
	if err != nil {
		return nil, err
	}

	// Build result in MCP format
	content := []map[string]interface{}{
		{
			"type": "text",
			"text": result.HTML,
		},
	}

	// Add screenshot as image content if applicable
	includeScreenshot := t.shouldIncludeScreenshot(opts.Screenshot, opts.ScreenshotMode, result.HTML)
	if includeScreenshot && len(result.Screenshot) > 0 {
		content = append(content, map[string]interface{}{
			"type":     "image",
			"data":     result.Screenshot,
			"mimeType": "image/png",
		})
	}

	// Determine content type based on format
	contentType := "text/html"
	if result.Format == "markdown" {
		contentType = "text/markdown"
	}

	metadata := map[string]interface{}{
		"url":          result.URL,
		"final_url":    RedactURL(result.FinalURL),
		"status_code":  result.StatusCode,
		"content_type": contentType,
		"size_bytes":   result.SizeBytes,
		"duration_ms":  result.Duration.Milliseconds(),
		"title":        result.Title,
		"rendering":    "javascript",
		"format":       result.Format,
		"method":       result.Method,
	}

	// Add named-session metadata when a session was used
	if opts.SessionID != "" {
		metadata["session_id"] = opts.SessionID
		metadata["session_reused"] = result.SessionReused
		if opts.CloseSession {
			metadata["session_closed"] = true
		}
	}

	// Add action metadata if interactive actions were executed
	if hasActions && result.ActionsMetadata != nil {
		metadata["interactive_actions"] = map[string]interface{}{
			"count":        result.ActionsMetadata.Count,
			"action_types": result.ActionsMetadata.Types,
			"cached":       false,
		}
		t.logger.Info().
			Int("actions_count", result.ActionsMetadata.Count).
			Msg("Interactive actions metadata added to result")
	}

	// #72: per-action outcomes (status / soft_timeout warning / failed error).
	// Lets the caller see what each action actually did, not just the overall
	// success/failure. In particular, a soft_timeout wait_for_text no longer
	// aborts the scrape — it appears here as a warning.
	if hasActions && len(result.ActionResults) > 0 {
		resultsMap := make([]map[string]interface{}, len(result.ActionResults))
		for i, r := range result.ActionResults {
			entry := map[string]interface{}{
				"index":    r.Index,
				"type":     r.Type,
				"status":   r.Status,
				"attempts": r.Attempts,
			}
			if r.Selector != "" {
				entry["selector"] = r.Selector
			}
			if r.Warning != "" {
				entry["warning"] = r.Warning
			}
			if r.Error != "" {
				entry["error"] = r.Error
			}
			resultsMap[i] = entry
		}
		metadata["action_results"] = resultsMap
	}

	// #72: page observations after mutating actions (only when observe_changes
	// was requested). Compact snapshots (URL, title, headings, errors,
	// body_changed, text_preview) so the LLM can judge whether an action had
	// its intended effect without guessing text to wait_for.
	if hasActions && len(result.ActionObservations) > 0 {
		obsMap := make([]map[string]interface{}, len(result.ActionObservations))
		for i, o := range result.ActionObservations {
			entry := map[string]interface{}{
				"action_index": o.ActionIndex,
				"url":          o.URL,
				"url_changed":  o.URLChanged,
				"title":        o.Title,
				"body_changed": o.BodyChanged,
			}
			if len(o.Headings) > 0 {
				entry["headings"] = o.Headings
			}
			if len(o.Errors) > 0 {
				entry["errors"] = o.Errors
			}
			if o.TextPreview != "" {
				entry["text_preview"] = o.TextPreview
			}
			obsMap[i] = entry
		}
		metadata["action_observations"] = obsMap
		t.logger.Info().
			Int("observations_count", len(result.ActionObservations)).
			Msg("Page observations added to result")
	}

	// Add network summary to metadata (#77): CDP-observed real HTTP status
	// codes, auth failures (401/403), CDN-blocking flags. Lets the LLM see
	// WHY a page is auth-required or blocked without guessing. Always present
	// for Chrome scrapes (the monitor is best-effort — nil only if CDP start
	// failed, in which case nothing is added).
	if result.NetworkSummary != nil {
		ns := result.NetworkSummary
		netMap := map[string]interface{}{
			"total_requests": ns.TotalRequests,
		}
		if ns.BlockedByCDN {
			netMap["blocked_by_cdn"] = true
		}
		if len(ns.AuthFailures) > 0 {
			authFails := make([]map[string]interface{}, len(ns.AuthFailures))
			for i, af := range ns.AuthFailures {
				authFails[i] = map[string]interface{}{
					"url":    af.URL,
					"status": af.Status,
				}
			}
			netMap["auth_failures"] = authFails
			// Compact human-readable hint for the LLM.
			netMap["auth_hint"] = ns.AuthFailureHint()
		}
		if len(ns.Requests) > 0 {
			reqs := make([]map[string]interface{}, len(ns.Requests))
			for i, r := range ns.Requests {
				reqs[i] = map[string]interface{}{
					"url":    r.URL,
					"status": r.Status,
				}
			}
			netMap["requests"] = reqs
		}
		metadata["network_summary"] = netMap
		t.logger.Debug().
			Int64("total_requests", ns.TotalRequests).
			Int("auth_failures", len(ns.AuthFailures)).
			Bool("blocked_by_cdn", ns.BlockedByCDN).
			Msg("Network summary added to metadata")
	}

	// Add DOM signals to metadata (#77): read-only page classification —
	// login-form presence, SPA/framework markers, anti-bot challenge hints.
	// Passive observations that help the LLM decide how to proceed (supply
	// credentials, expect async hydration, retry differently). They never
	// alter the scrape flow. Nil for HTTP-only scrapes.
	if result.DOMSignals != nil {
		ds := result.DOMSignals
		dsMap := map[string]interface{}{}
		if ds.HasLoginForm {
			dsMap["has_login_form"] = true
		}
		if ds.IsSPA {
			dsMap["is_spa"] = true
		}
		if ds.Framework != "" {
			dsMap["framework"] = ds.Framework
		}
		if ds.BlockedHint != "" {
			dsMap["blocked_hint"] = ds.BlockedHint
		}
		// Only attach when at least one signal fired — an empty map adds noise.
		if len(dsMap) > 0 {
			metadata["dom_signals"] = dsMap
			t.logger.Debug().
				Bool("has_login_form", ds.HasLoginForm).
				Bool("is_spa", ds.IsSPA).
				Str("framework", ds.Framework).
				Str("blocked_hint", ds.BlockedHint).
				Msg("DOM signals added to metadata")
		}
	}

	// Add login result to metadata (#77 Tier-2): the verdict + evidence of a
	// login composite action. Lets the LLM see WHY auth succeeded or failed
	// (URL changed, cookies grew, auth keys appeared) instead of guessing.
	// Nil when no login action ran.
	if result.LoginResult != nil {
		lr := result.LoginResult
		loginMap := map[string]interface{}{
			"status": string(lr.Status),
		}
		if len(lr.Evidence) > 0 {
			loginMap["evidence"] = lr.Evidence
		}
		if lr.SubmitSelectorUsed != "" {
			loginMap["submit_selector_used"] = lr.SubmitSelectorUsed
		}
		metadata["login_result"] = loginMap
		t.logger.Info().
			Str("login_status", string(lr.Status)).
			Strs("evidence", lr.Evidence).
			Msg("Login result added to metadata")
	}

	// Add execute_js results to metadata if any execute_js actions were run
	if len(result.JSResults) > 0 {
		jsResultsMap := make([]map[string]interface{}, len(result.JSResults))
		for i, jr := range result.JSResults {
			entry := map[string]interface{}{
				"action_index": jr.ActionIndex,
			}
			if jr.Err != nil {
				entry["error"] = jr.Err.Error()
			} else {
				entry["result"] = jr.Result
			}
			jsResultsMap[i] = entry
		}
		metadata["execute_js_results"] = jsResultsMap
		t.logger.Info().
			Int("js_results_count", len(result.JSResults)).
			Msg("execute_js results added to metadata")
	}

	if result.FromCache {
		metadata["cached"] = true
		metadata["duration_ms"] = 0
	}

	// Surface HTTP-fallback provenance (#49). When Chrome failed and the body
	// came from a plain HTTP request, warn the client: this is not the
	// JS-rendered page they asked for and (after an action_error) lacks session
	// state. Even a large body may be missing dynamic content.
	if result.FallbackReason != "" {
		metadata["fallback_reason"] = result.FallbackReason
		metadata["fallback_warning"] = result.FallbackWarning
	}

	if includeScreenshot && len(result.Screenshot) > 0 {
		metadata["screenshot_included"] = true
		metadata["screenshot_size"] = len(result.Screenshot)
	}

	// Auto-index in RAG (background, non-blocking)
	if t.ragConfig.Enabled {
		go t.indexToRAG(urlStr, result)
	}

	t.logger.Info().
		Str("url", urlStr).
		Str("final_url", RedactURL(result.FinalURL)).
		Int("size_bytes", result.SizeBytes).
		Int64("duration_ms", result.Duration.Milliseconds()).
		Bool("screenshot_included", includeScreenshot && len(result.Screenshot) > 0).
		Msg("Successfully scraped URL with JavaScript")

	return map[string]interface{}{
		"content":   content,
		"_metadata": metadata,
	}, nil
}

// buildOptions converts args to Options
func (t *ScrapeJSTool) buildOptions(args map[string]interface{}, actions []browser.Action) Options {
	// Extract timeout
	timeout := 60
	if timeoutSec, ok := args["timeout"].(float64); ok {
		timeout = int(timeoutSec)
	}

	// Extract wait_for
	waitFor := ""
	if wf, ok := args["wait_for"].(string); ok {
		waitFor = wf
	}

	// Extract wait_time
	waitTime := 3000
	if wt, ok := args["wait_time"].(float64); ok {
		waitTime = int(wt)
	}

	// Extract wait_for_network_idle
	waitForNetworkIdle := false
	if wfi, ok := args["wait_for_network_idle"].(bool); ok {
		waitForNetworkIdle = wfi
	}

	// Extract screenshot
	screenshot := false
	if ss, ok := args["screenshot"].(bool); ok {
		screenshot = ss
	}

	// Extract screenshot_mode
	screenshotMode := "auto"
	if sm, ok := args["screenshot_mode"].(string); ok {
		screenshotMode = sm
	}

	// Extract output_format
	outputFormat := "markdown" // Default to markdown for 75% token savings
	if of, ok := args["output_format"].(string); ok {
		outputFormat = of
	}

	// Extract user_agent
	userAgent := ""
	if ua, ok := args["user_agent"].(string); ok {
		userAgent = ua
	}

	// Extract viewport
	viewportWidth := 1920
	if vw, ok := args["viewport_width"].(float64); ok {
		viewportWidth = int(vw)
	}

	viewportHeight := 1080
	if vh, ok := args["viewport_height"].(float64); ok {
		viewportHeight = int(vh)
	}

	// Extract block_images
	blockImages := false
	if bi, ok := args["block_images"].(bool); ok {
		blockImages = bi
	}

	// Extract stealth settings
	stealthEnabled := false
	if se, ok := args["stealth_enabled"].(bool); ok {
		stealthEnabled = se
	}

	stealthScroll := true
	if ss, ok := args["stealth_scroll"].(bool); ok {
		stealthScroll = ss
	}

	stealthMouse := false
	if sm, ok := args["stealth_mouse"].(bool); ok {
		stealthMouse = sm
	}

	// Extract named session settings
	sessionID := ""
	if sid, ok := args["session_id"].(string); ok {
		sessionID = sid
	}

	closeSession := false
	if cs, ok := args["close_session"].(bool); ok {
		closeSession = cs
	}

	// Extract observe_changes (#72): opt-in compact page snapshots after
	// mutating actions, returned in metadata.action_observations.
	observeChanges := false
	if oc, ok := args["observe_changes"].(bool); ok {
		observeChanges = oc
	}

	// Extract smart_settle (#71): post-action DOM-stability wait. Default ON
	// when actions are present (the tool is helpful by default — an async auth
	// fetch or SPA route change needs time to settle, and snapshotting the
	// instant a click returns captures an empty/stale state). Explicitly
	// settable to false to force instant snapshots.
	smartSettle := len(actions) > 0
	if smartSettle {
		if ss, ok := args["smart_settle"].(bool); ok {
			smartSettle = ss
		}
	}

	return Options{
		Timeout:            time.Duration(timeout) * time.Second,
		WaitForSelector:    waitFor,
		WaitForDuration:    time.Duration(waitTime) * time.Millisecond,
		WaitForNetworkIdle: waitForNetworkIdle,
		Screenshot:         screenshot,
		ScreenshotMode:     screenshotMode,
		UserAgent:          userAgent,
		ViewportWidth:      viewportWidth,
		ViewportHeight:     viewportHeight,
		BlockImages:        blockImages,
		OutputFormat:       outputFormat,
		StealthEnabled:     stealthEnabled,
		StealthScroll:      stealthScroll,
		StealthMouse:       stealthMouse,
		Actions:            actions,
		SessionID:          sessionID,
		CloseSession:       closeSession,
		ObserveChanges:     observeChanges,
		SmartSettle:        smartSettle,
	}
}

// shouldIncludeScreenshot determines whether to include screenshot based on mode and HTML size
func (t *ScrapeJSTool) shouldIncludeScreenshot(screenshot bool, screenshotMode string, html string) bool {
	if screenshot {
		return true
	}

	if screenshotMode == "always" {
		return true
	}

	if screenshotMode == "auto" {
		// Include screenshot if HTML is large (> 50KB)
		return len(html) > 50*1024
	}

	return false
}

// indexToRAG indexes the scraped content to RAG in the background
func (t *ScrapeJSTool) indexToRAG(url string, result *Result) {
	// Prepare index request
	indexReq := map[string]interface{}{
		"url":             url,
		"processing_mode": "structured",
		"ttl":             7,
	}

	jsonData, _ := json.Marshal(indexReq)
	indexURL := t.ragConfig.BaseURL + "/api/v1/index"

	// Retry logic
	var lastErr error
	for attempt := 0; attempt <= t.ragConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry
			time.Sleep(time.Duration(t.ragConfig.RetryDelay) * time.Second)
			t.logger.Debug().
				Str("url", url).
				Int("attempt", attempt).
				Msg("Retrying RAG index")
		}

		// Index in background (don't block response)
		resp, err := http.Post(
			indexURL,
			"application/json",
			strings.NewReader(string(jsonData)),
		)
		if err != nil {
			lastErr = err
			t.logger.Warn().
				Str("url", url).
				Int("attempt", attempt).
				Err(err).
				Msg("RAG auto-index attempt failed")
			continue
		}

		// Success
		resp.Body.Close()
		t.logger.Info().
			Str("url", url).
			Int("status", resp.StatusCode).
			Int("attempt", attempt).
			Msg("RAG auto-indexed")
		return
	}

	// All retries failed
	t.logger.Error().
		Str("url", url).
		Err(lastErr).
		Int("max_retries", t.ragConfig.MaxRetries).
		Msg("RAG auto-index failed after all retries")
}
