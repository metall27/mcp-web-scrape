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
				// Plain (non-UNMASKED) fingerprint branch — must not read "0".
				webgl.version = String(gl.getParameter(gl.VERSION));
				webgl.shading = String(gl.getParameter(gl.SHADING_LANGUAGE_VERSION));
				webgl.vendor_plain = String(gl.getParameter(gl.VENDOR));
				webgl.no_throw = (function(){ try { gl.clearColor(0,0,0,1); return true; } catch(e){ return false; } })();
				// Native shape (#101 review warning 1).
				webgl.is_instance = gl instanceof WebGLRenderingContext;
				webgl.tag = Object.prototype.toString.call(gl);
				webgl.const_version = gl.VERSION;      // inherited named constant
				webgl.const_max_tex = gl.MAX_TEXTURE_SIZE;
				// getParameter must be proto-level (own list empty-ish).
				webgl.getparam_own = Object.prototype.hasOwnProperty.call(gl, 'getParameter');
				webgl.getparam_src = String(gl.getParameter).slice(0, 60);
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
				proto_getter: !!(Object.getOwnPropertyDescriptor(Navigator.prototype, 'plugins') || {}).get,
				// Native descriptor shape (#101 review warning 2): length/item
				// must come from the PROTOTYPE, and member fields (name) must
				// be proto getters, not own data props.
				length_own: Object.prototype.hasOwnProperty.call(p, 'length'),
				length_on_proto: !!(Object.getOwnPropertyDescriptor(PluginArray.prototype, 'length') || {}).get,
				item_own: Object.prototype.hasOwnProperty.call(p, 'item'),
				item_on_proto: !!(Object.getOwnPropertyDescriptor(PluginArray.prototype, 'item') || {}).value,
				name_own: p.length > 0 && Object.prototype.hasOwnProperty.call(p[0], 'name'),
				name_on_proto: !!(Object.getOwnPropertyDescriptor(Plugin.prototype, 'name') || {}).get,
				name_value: p.length > 0 ? p[0].name : null,
				item0: p.item(0) instanceof Plugin
			};
			// --- toString disguise (nuance) ---
			const wdGet = (Object.getOwnPropertyDescriptor(Navigator.prototype, 'webdriver') || {}).get;
			const nuance = {
				wd_toString: wdGet ? String(wdGet) : null,
				wd_fts: wdGet ? Function.prototype.toString.call(wdGet) : null
			};
			// --- window.chrome mock (stage 5) ---
			// Ground truth: window.chrome top-level = {loadTimes, csi, app};
			// 'app' is NOT mocked (engine overwrites it with its native
			// 7-prop one — review M1), so the live app must be the NATIVE
			// shape. csi/loadTimes are mocked with WeakMap-only disguise
			// (review M2): no own toString, own names exactly
			// [length, name, prototype].
			const cObj = window.chrome;
			const wd = Object.getOwnPropertyDescriptor(window, 'chrome');
			const cm = cObj ? {
				keys: Object.keys(cObj).sort().join(','),
				runtime_type: typeof cObj.runtime,
				webstore_type: typeof cObj.webstore,
				app_props: cObj.app ? Object.getOwnPropertyNames(cObj.app).sort().join(',') : null,
				app_getdetails_src: cObj.app ? String(cObj.app.getDetails).slice(0, 45) : null,
				csi_type: typeof cObj.csi,
				loadtimes_type: typeof cObj.loadTimes,
				csi_calls: (function(){ try { const r = cObj.csi(); return typeof r.startE === 'number' && typeof r.tran === 'number'; } catch(e){ return 'throw'; } })(),
				csi_src: String(cObj.csi).slice(0, 50),
				lt_src: String(cObj.loadTimes).slice(0, 50),
				csi_own: Object.getOwnPropertyNames(cObj.csi).sort().join(','),
				lt_own: Object.getOwnPropertyNames(cObj.loadTimes).sort().join(','),
				lt_fields: (function(){ try { const r = cObj.loadTimes(); return typeof r.commitLoadTime === 'number' && 'connectionInfo' in r && 'npnNegotiatedProtocol' in r; } catch(e){ return 'throw'; } })(),
				desc: wd ? {writable: wd.writable, enumerable: wd.enumerable, configurable: wd.configurable, has_get: !!wd.get} : null
			} : null;
			return JSON.stringify({webgl: webgl, plugins: plugins, nuance: nuance, chrome_mock: cm});
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
		// Plain fingerprint branch (#101 review warning 1): version/shading
		// must be real strings, not "0" (the old plain-object fake had no
		// named constants and returned 0 here).
		if !strings.Contains(probe, `"version":"WebGL 1.0`) {
			t.Errorf("gl.getParameter(gl.VERSION) does not return a real version string:\n%s", probe)
		}
		if !strings.Contains(probe, `"shading":"WebGL GLSL ES 1.0`) {
			t.Errorf("gl.getParameter(gl.SHADING_LANGUAGE_VERSION) is not a real string:\n%s", probe)
		}
		if !strings.Contains(probe, `"vendor_plain":"`+profile.WebGLVendor+`"`) {
			t.Errorf("gl.getParameter(gl.VENDOR) does not mirror profile vendor:\n%s", probe)
		}
		// Native shape: instanceof, tag, inherited constants, proto-level
		// getParameter with native-looking source.
		if !strings.Contains(probe, `"is_instance":true`) {
			t.Errorf("fake gl not instanceof WebGLRenderingContext:\n%s", probe)
		}
		if !strings.Contains(probe, `"tag":"[object WebGLRenderingContext]"`) {
			t.Errorf("Object.prototype.toString.call(gl) is not [object WebGLRenderingContext]:\n%s", probe)
		}
		if !strings.Contains(probe, `"const_version":7938`) || !strings.Contains(probe, `"const_max_tex":3379`) {
			t.Errorf("named constants gl.VERSION/gl.MAX_TEXTURE_SIZE not inherited:\n%s", probe)
		}
		if !strings.Contains(probe, `"getparam_own":false`) {
			t.Errorf("getParameter is an own prop on the fake context (must be proto-level):\n%s", probe)
		}
		if !strings.Contains(probe, `"getparam_src":"function getParameter() { [native code] }"`) {
			t.Errorf("getParameter source not disguised as native:\n%s", probe)
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
	// Native descriptor shape (#101 review warning 2): length/item/name must
	// live on the prototypes as getter/method, not as own data props.
	if !strings.Contains(probe, `"length_own":false`) || !strings.Contains(probe, `"length_on_proto":true`) {
		t.Errorf("plugins.length must be a PROTOTYPE getter (real Chrome shape):\n%s", probe)
	}
	if !strings.Contains(probe, `"item_own":false`) || !strings.Contains(probe, `"item_on_proto":true`) {
		t.Errorf("plugins.item must be a PROTOTYPE method:\n%s", probe)
	}
	if !strings.Contains(probe, `"name_own":false`) || !strings.Contains(probe, `"name_on_proto":true`) {
		t.Errorf("plugin.name must be a PROTOTYPE getter:\n%s", probe)
	}
	if !strings.Contains(probe, `"name_value":"Chrome PDF Plugin"`) {
		t.Errorf("plugin[0].name value lost in the proto-getter rewrite:\n%s", probe)
	}
	if !strings.Contains(probe, `"item0":true`) {
		t.Errorf("plugins.item(0) does not return a Plugin:\n%s", probe)
	}

	// Nuance: override getters must look native — both own toString AND
	// Function.prototype.toString.call (the path fp-collect uses).
	if !strings.Contains(probe, `"wd_toString":"function get webdriver() { [native code] }"`) {
		t.Errorf("webdriver getter own toString not masked:\n%s", probe)
	}
	if !strings.Contains(probe, `"wd_fts":"function get webdriver() { [native code] }"`) {
		t.Errorf("Function.prototype.toString.call(webdriverGetter) not masked — fp-collect would see the override source:\n%s", probe)
	}

	// Stage 5: window.chrome mock must mirror the NATIVE headless shape.
	if !strings.Contains(probe, `"keys":"app,csi,loadTimes"`) {
		t.Errorf("window.chrome keys must be exactly {app,csi,loadTimes} (native headless shape):\n%s", probe)
	}
	if !strings.Contains(probe, `"runtime_type":"undefined"`) {
		t.Errorf("window.chrome.runtime must be absent (native headless has none; runtime:{} was the fp-collect tell):\n%s", probe)
	}
	if !strings.Contains(probe, `"webstore_type":"undefined"`) {
		t.Errorf("window.chrome.webstore must be undefined (native headless ground truth):\n%s", probe)
	}
	// M1: 'app' is NOT mocked — the live app must be the ENGINE's native
	// 7-prop one (installState/runningState present, native getDetails).
	if !strings.Contains(probe, `"app_props":"InstallState,RunningState,getDetails,getIsInstalled,installState,isInstalled,runningState"`) {
		t.Errorf("window.chrome.app is not the engine's native 7-prop app (was the mock's app overwritten?):\n%s", probe)
	}
	if !strings.Contains(probe, `"app_getdetails_src":"function getDetails() { [native code] }"`) {
		t.Errorf("window.chrome.app.getDetails is not the native function (engine app expected, not mock app):\n%s", probe)
	}
	if !strings.Contains(probe, `"csi_calls":true`) || !strings.Contains(probe, `"lt_fields":true`) {
		t.Errorf("chrome.csi()/loadTimes() must return native-shaped values:\n%s", probe)
	}
	if !strings.Contains(probe, `"csi_src":"function csi() { [native code] }"`) ||
		!strings.Contains(probe, `"lt_src":"function loadTimes() { [native code] }"`) {
		t.Errorf("csi/loadTimes toString not masked (via global FTS interceptor):\n%s", probe)
	}
	// M2: no per-function own toString — own names must be exactly the
	// native triple [length, name, prototype].
	if !strings.Contains(probe, `"csi_own":"length,name,prototype"`) ||
		!strings.Contains(probe, `"lt_own":"length,name,prototype"`) {
		t.Errorf("csi/loadTimes carry extra own props (own toString tell — must be WeakMap-only disguise):\n%s", probe)
	}
	if !strings.Contains(probe, `"desc":{"writable":true,"enumerable":true,"configurable":false,"has_get":false}`) {
		t.Errorf("window.chrome descriptor must match native (writable+enumerable, non-configurable, data prop):\n%s", probe)
	}
}
