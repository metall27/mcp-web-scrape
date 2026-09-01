package browser

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestStealthWebdriverHiddenOnTargetPage (#101 stage 2): the full
// production stealth chain must leave navigator.webdriver in the SAME
// shape a real Chrome has it: NO own property on the navigator instance,
// native-looking getter on Navigator.prototype returning false.
//
// History: the old override defined an OWN property returning undefined —
// the value read fine, but `_.has(navigator, "webdriver")` /
// `Object.prototype.hasOwnProperty.call(navigator, 'webdriver')` returned
// true, which bot.sannysoft flags as "WebDriver (New): present (failed)".
// Real detection checks the property SHAPE, not just the value.
func TestStealthWebdriverHiddenOnTargetPage(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool, err := New(Config{MaxTabs: 1, Headless: true, NoSandbox: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bctx, bcancel, err := pool.GetContext(ctx)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	defer bcancel()

	profile := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	stealth := NewStealthActions(StealthConfig{})

	var probe string
	err = chromedp.Run(bctx,
		chromedp.Navigate("about:blank"),
		// The inject must run INSIDE chromedp.Run (a bare .Do on the pool
		// context errors with "invalid context" — actions need an active tab).
		stealth.InjectAntiDetectionScripts(profile),
		chromedp.Reload(),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(`(() => {
			const own = Object.getOwnPropertyDescriptor(navigator, 'webdriver');
			const d = Object.getOwnPropertyDescriptor(Navigator.prototype, 'webdriver');
			return JSON.stringify({
				wd: String(navigator.webdriver),
				// sannysoft: navigator.webdriver || _.has(navigator, "webdriver")
				has_own: Object.prototype.hasOwnProperty.call(navigator, 'webdriver'),
				in_op: 'webdriver' in navigator,
				own_present: !!own,
				proto_present: !!d,
				proto_returns_false: d ? d.get() === false : null,
				// Full native-descriptor shape (measured on the Docker
				// test-stage Chromium by the PR #102 review): a real Chrome
				// exposes {enumerable:true, configurable:true, hasGet:true,
				// hasSet:false}. Any future drift (e.g. enumerable:false)
				// must fail here, not in production detection.
				desc_enumerable: d ? d.enumerable : null,
				desc_configurable: d ? d.configurable : null,
				desc_has_get: d ? typeof d.get === 'function' : null,
				desc_has_set: d ? d.set !== undefined : null
			});
		})()`, &probe),
	)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("probe: %s", probe)

	if strings.Contains(probe, `"wd":"true"`) {
		t.Errorf("navigator.webdriver === true:\n%s", probe)
	}
	// The detection SHAPE: no own property, no hasOwnProperty hit.
	if !strings.Contains(probe, `"has_own":false`) {
		t.Errorf("navigator has OWN webdriver property — _.has(navigator,'webdriver') style checks will flag it:\n%s", probe)
	}
	if !strings.Contains(probe, `"own_present":false`) {
		t.Errorf("own descriptor present on navigator instance:\n%s", probe)
	}
	if !strings.Contains(probe, `"proto_returns_false":true`) {
		t.Errorf("prototype getter must return false (real Chrome semantics):\n%s", probe)
	}
	// Native descriptor shape (review-verified reference).
	if !strings.Contains(probe, `"desc_enumerable":true`) ||
		!strings.Contains(probe, `"desc_configurable":true`) ||
		!strings.Contains(probe, `"desc_has_get":true`) ||
		!strings.Contains(probe, `"desc_has_set":false`) {
		t.Errorf("prototype descriptor shape is not native {e:true,c:true,get:true,set:false}:\n%s", probe)
	}
}
