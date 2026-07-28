package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// LoginStatus is the outcome verdict of a login action.
type LoginStatus string

const (
	LoginStatusSuccess    LoginStatus = "success"
	LoginStatusAmbiguous  LoginStatus = "ambiguous" // submitted but signals inconclusive
	LoginStatusAuthFailed LoginStatus = "auth_failed"
)

// LoginResult is the structured outcome of the login composite action (#77
// Tier-2). Unlike a manual type→type→click chain that returns only success or
// error, login returns a rich verdict with evidence so the LLM understands WHY
// authentication succeeded or failed and can decide on next steps (retry with
// different credentials, check selectors, use a named session).
//
// Stashed on the executor and surfaced in metadata via PageObservation so the
// normal observation/feedback loop carries it to the LLM.
type LoginResult struct {
	// Status is the verdict: success / ambiguous / auth_failed.
	Status LoginStatus `json:"status"`
	// Evidence are the concrete signals behind the verdict, e.g.
	// ["URL changed from /login to /dashboard", "cookies 0→2",
	// "auth-store key appeared"]. Qualitative, no numeric confidence — per
	// the #77 consensus that evidence strings beat opaque scores.
	Evidence []string `json:"evidence,omitempty"`
	// SubmitSelectorUsed is the submit selector actually used (the one passed
	// in, or the auto-detected button[type=submit] when omitted).
	SubmitSelectorUsed string `json:"submit_selector_used,omitempty"`
}

// loginProbeResult is the raw JS probe after submit.
type loginProbeResult struct {
	URLBefore      string   `json:"url_before"`
	URLAfter       string   `json:"url_after"`
	TitleAfter     string   `json:"title_after"`
	CookiesBefore  int      `json:"cookies_before"`
	CookiesAfter   int      `json:"cookies_after"`
	HasLoginForm   bool     `json:"has_login_form"`
	AuthKeysBefore []string `json:"auth_keys_before"`
	AuthKeysAfter  []string `json:"auth_keys_after"`
}

// ExecuteLogin is the login composite action (#77 Tier-2). It:
//  1. Records pre-submit state (URL, cookies, auth-like localStorage keys).
//  2. Fills username + password fields (reusing ExecuteType for parity with the
//     human-typed path, including stealth if enabled).
//  3. Submits (explicit selector or auto-detected button[type=submit] nearest
//     the password field).
//  4. Waits for the page to settle (the surrounding ExecuteActions loop also
//     smart-settles after, but we want a fresh state before the verify probe).
//  5. Verifies: did the URL leave the login form, did cookies/auth-keys grow,
//     does a password field still exist? Builds LoginResult with evidence.
//
// login is an explicit mutation — it only runs when the LLM asks for it with
// real selectors + credentials. It never auto-detects credentials or guesses
// field names (the "smart_login" approach explicitly rejected in #77).
func (e *ActionExecutor) ExecuteLogin(ctx context.Context, action Action) error {
	// 1. Capture pre-submit state.
	probeBefore := e.loginProbe(ctx, "before")

	// 2. Fill username field.
	e.logger.Debug().Str("selector", action.UsernameSelector).Msg("Login: filling username")
	if err := e.ExecuteType(ctx, action.UsernameSelector, action.Username); err != nil {
		return fmt.Errorf("login username field: %w", err)
	}

	// 3. Fill password field.
	e.logger.Debug().Str("selector", action.PasswordSelector).Msg("Login: filling password")
	if err := e.ExecuteType(ctx, action.PasswordSelector, action.Password); err != nil {
		return fmt.Errorf("login password field: %w", err)
	}

	// 4. Resolve submit selector. When omitted, auto-detect button[type=submit]
	//    nearest the password field. We never guess a login button by text.
	submitSel := action.SubmitSelector
	if submitSel == "" {
		detected, derr := e.detectSubmitSelector(ctx, action.PasswordSelector)
		if derr != nil {
			return fmt.Errorf("login submit selector not provided and auto-detect failed: %w", derr)
		}
		submitSel = detected
		e.logger.Debug().Str("selector", submitSel).Msg("Login: auto-detected submit button")
	}

	// 5. Click submit.
	e.logger.Debug().Str("selector", submitSel).Msg("Login: clicking submit")
	if err := e.ExecuteClick(ctx, submitSel); err != nil {
		return fmt.Errorf("login submit click: %w", err)
	}

	// 6. Give the page a moment to react to the submit before the verify probe.
	//    The surrounding ExecuteActions loop runs smart-settle after this action
	//    returns, which handles the longer SPA hydration wait. Here we only need
	//    enough time for the auth fetch / redirect to kick off.
	time.Sleep(500 * time.Millisecond)

	// 7. Verify: probe post-submit state and build the verdict.
	result := e.verifyLogin(ctx, action, submitSel, probeBefore)
	e.lastLoginResult = &result

	e.logger.Info().
		Str("login_status", string(result.Status)).
		Strs("evidence", result.Evidence).
		Msg("Login composite action completed")

	// login is NOT a hard error even on auth_failed — the page still loaded
	// (likely back on the login form with an error message) and the LLM should
	// see it via observations + the LoginResult. Returning nil lets the action
	// chain continue; the rich feedback in metadata is the point.
	return nil
}

// detectSubmitSelector finds a submit button when SubmitSelector is omitted.
// Strategy: button[type=submit] or input[type=submit] inside the same <form>
// as the password field. Falls back to the first such button on the page.
func (e *ActionExecutor) detectSubmitSelector(ctx context.Context, passwordSel string) (string, error) {
	var sel string
	js := fmt.Sprintf(`(() => {
		const pw = document.querySelector(%s);
		const form = pw ? pw.closest('form') : null;
		const inForm = form ? form.querySelector('button[type="submit"], input[type="submit"]') : null;
		if (inForm) return 'button';
		// Fallback: first submit button on the page.
		const any = document.querySelector('button[type="submit"], input[type="submit"]');
		return any ? 'page' : 'none';
	})()`, quoteSelector(passwordSel))
	var where string
	if err := chromedp.Evaluate(js, &where).Do(ctx); err != nil {
		return "", fmt.Errorf("submit auto-detect probe failed: %w", err)
	}
	switch where {
	case "button":
		sel = passwordSel + " ~ button[type=submit], " + passwordSel + " ~ input[type=submit]"
		// The closest(form) approach found one; use a form-scoped CSS path.
		sel = fmt.Sprintf(`form:has(%s) button[type="submit"], form:has(%s) input[type="submit"]`,
			strings.TrimLeft(passwordSel, "> "), strings.TrimLeft(passwordSel, "> "))
	case "page":
		sel = `button[type="submit"], input[type="submit"]`
	default:
		return "", fmt.Errorf("no submit button found near %s", passwordSel)
	}
	return sel, nil
}

// verifyLogin probes the post-submit page and builds a LoginResult verdict.
func (e *ActionExecutor) verifyLogin(ctx context.Context, action Action, submitSel string, before loginProbeResult) LoginResult {
	probeAfter := e.loginProbe(ctx, "after")

	var evidence []string
	urlChanged := before.URLBefore != "" && probeAfter.URLAfter != before.URLBefore
	if urlChanged {
		evidence = append(evidence, fmt.Sprintf("URL changed from %s to %s",
			shortURL(before.URLBefore), shortURL(probeAfter.URLAfter)))
	}
	if probeAfter.CookiesAfter > before.CookiesBefore {
		evidence = append(evidence, fmt.Sprintf("cookies %d→%d",
			before.CookiesBefore, probeAfter.CookiesAfter))
	}
	newAuthKeys := diffKeys(before.AuthKeysBefore, probeAfter.AuthKeysAfter)
	if len(newAuthKeys) > 0 {
		evidence = append(evidence, fmt.Sprintf("auth keys appeared: %s", strings.Join(newAuthKeys, ", ")))
	}
	// login form no longer present (was present before, gone after) → strong success signal
	if before.HasLoginForm && !probeAfter.HasLoginForm {
		evidence = append(evidence, "login form disappeared")
	}

	var status LoginStatus
	switch {
	case len(evidence) > 0 && !probeAfter.HasLoginForm:
		status = LoginStatusSuccess
	case len(evidence) > 0:
		// some signals but login form still present — ambiguous
		status = LoginStatusAmbiguous
		evidence = append(evidence, "login form still present")
	case probeAfter.HasLoginForm:
		status = LoginStatusAuthFailed
		evidence = append(evidence, "stayed on login form, no auth signals")
	default:
		status = LoginStatusAmbiguous
	}

	return LoginResult{
		Status:             status,
		Evidence:           evidence,
		SubmitSelectorUsed: submitSel,
	}
}

// loginProbe captures auth-relevant state at a point in time. tag is "before"
// or "after" for logging only.
func (e *ActionExecutor) loginProbe(ctx context.Context, tag string) loginProbeResult {
	var probe loginProbeResult
	js := `(() => {
		let cookies = 0;
		try { cookies = document.cookie.length; } catch(e) {}
		const authKeys = [];
		try {
			const re = /(auth|token|session|user|jwt|bearer)/i;
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				if (k && re.test(k)) authKeys.push(k);
			}
		} catch(e) {}
		return {
			url_before: location.href,
			url_after: location.href,
			title_after: document.title || '',
			cookies_before: cookies,
			cookies_after: cookies,
			has_login_form: !!document.querySelector('input[type="password"]'),
			auth_keys_before: authKeys,
			auth_keys_after: authKeys
		};
	})()`
	if err := chromedp.Evaluate(js, &probe).Do(ctx); err != nil {
		e.logger.Debug().Err(err).Str("tag", tag).Msg("login probe failed (non-critical)")
	}
	return probe
}

// diffKeys returns keys in after that are not in before.
func diffKeys(before, after []string) []string {
	seen := make(map[string]bool, len(before))
	for _, k := range before {
		seen[k] = true
	}
	var diff []string
	for _, k := range after {
		if !seen[k] {
			diff = append(diff, k)
		}
	}
	return diff
}

// shortURL strips the scheme for compact evidence strings.
func shortURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		return u[i+3:]
	}
	return u
}
