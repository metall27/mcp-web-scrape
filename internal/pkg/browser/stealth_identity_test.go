package browser

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestStealthIdentityHardening (#101 stages 3-4 + the toString nuance):
// after the full stealth chain, the page must expose
//   - a WORKING WebGL context (stand-in when no GPU) whose
//     vendor/renderer match the profile;
//   - navigator.plugins that is a REAL PluginArray (tag check), with
//     Plugin instances and named access, served from a PROTOTYPE getter
//     (no instance-level own property);
//   - override getters whose toString masquerades as native code.
func TestStealthIdentityHardening(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool, err := New(Config{MaxTabs: 1, Headless: true, NoSandbox: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bctx, bcancel, err := pool.GetContext(ctx)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	defer bcancel()

	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	profile := NewProfile(ua)
	stealth := NewStealthActions(StealthConfig{})

	var probe string
	err = chromedp.Run(bctx,
		chromedp.Navigate("about:blank"),
		stealth.InjectAntiDetectionScripts(profile),
		chromedp.Reload(),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(`(() => {
			// --- WebGL (stage 3) ---
			const c = document.createElement('canvas');
			const gl = c.getContext('webgl');
			let webgl = { present: !!gl };
			if (gl) {
				const ext = gl.getExtension('WEBGL_debug_renderer_info');
				webgl.vendor = ext ? String(gl.getParameter(ext.UNMASKED_VENDOR_WEBGL)) : null;
				webgl.renderer = ext ? String(gl.getParameter(ext.UNMASKED_RENDERER_WEBGL)) : null;
				webgl.version = String(gl.getParameter(gl.VERSION));
				webgl.no_throw = (function(){ try { gl.clearColor(0,0,0,1); return true; } catch(e){ return false; } })();
			}
			// --- PluginArray (stage 4) ---
			const p = navigator.plugins;
			const plugins = {
				tag: Object.prototype.toString.call(p),
				is_plugin_array: p instanceof PluginArray,
				len: p.length,
				first_is_plugin: p.length > 0 && (p[0] instanceof Plugin),
				named: p['Chrome PDF Plugin'] instanceof Plugin,
				own_on_instance: Object.prototype.hasOwnProperty.call(navigator, 'plugins'),
				proto_getter: !!(Object.getOwnPropertyDescriptor(Navigator.prototype, 'plugins') || {}).get
			};
			// --- toString disguise (nuance) ---
			const wdGet = (Object.getOwnPropertyDescriptor(Navigator.prototype, 'webdriver') || {}).get;
			const nuance = {
				wd_toString: wdGet ? String(wdGet) : null,
				wd_fts: wdGet ? Function.prototype.toString.call(wdGet) : null
			};
			return JSON.stringify({webgl: webgl, plugins: plugins, nuance: nuance});
		})()`, &probe),
	)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("probe: %s", probe)

	// Stage 3: WebGL context must exist and be coherent with the profile.
	if !strings.Contains(probe, `"present":true`) {
		t.Errorf("WebGL context missing — 'Canvas has no webgl context' flag remains:\n%s", probe)
	} else {
		if !strings.Contains(probe, `"vendor":"`+profile.WebGLVendor+`"`) {
			t.Errorf("WebGL vendor does not match profile (%s):\n%s", profile.WebGLVendor, probe)
		}
		if !strings.Contains(probe, `"no_throw":true`) {
			t.Errorf("fake WebGL methods throw:\n%s", probe)
		}
	}

	// Stage 4: honest PluginArray.
	if !strings.Contains(probe, `"tag":"[object PluginArray]"`) {
		t.Errorf("plugins tag is not [object PluginArray]:\n%s", probe)
	}
	if !strings.Contains(probe, `"is_plugin_array":true`) ||
		!strings.Contains(probe, `"first_is_plugin":true`) ||
		!strings.Contains(probe, `"named":true`) {
		t.Errorf("plugins are not real Plugin instances:\n%s", probe)
	}
	if !strings.Contains(probe, `"own_on_instance":false`) {
		t.Errorf("plugins still defined as instance-level own property:\n%s", probe)
	}
	if !strings.Contains(probe, `"proto_getter":true`) {
		t.Errorf("plugins getter not on Navigator.prototype:\n%s", probe)
	}

	// Nuance: override getters must look native — both own toString AND
	// Function.prototype.toString.call (the path fp-collect uses).
	if !strings.Contains(probe, `"wd_toString":"function get webdriver() { [native code] }"`) {
		t.Errorf("webdriver getter own toString not masked:\n%s", probe)
	}
	if !strings.Contains(probe, `"wd_fts":"function get webdriver() { [native code] }"`) {
		t.Errorf("Function.prototype.toString.call(webdriverGetter) not masked — fp-collect would see the override source:\n%s", probe)
	}
}
