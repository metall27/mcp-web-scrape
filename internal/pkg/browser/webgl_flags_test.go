package browser

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// probeWebGL returns the WebGL availability + UNMASKED_VENDOR/RENDERER for
// the browser launched with the given extra flags.
func probeWebGL(t *testing.T, extraFlags []chromedp.ExecAllocatorOption, profile BrowserProfile) string {
	t.Helper()
	opts := append([]chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", "new"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	}, extraFlags...)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelT := context.WithTimeout(ctx, 45*time.Second)
	defer cancelT()

	var probe string
	err := chromedp.Run(ctx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := emulation.SetUserAgentOverride(profile.UserAgent).WithUserAgentMetadata(profile.UserAgentMetadata()).Do(ctx); err != nil {
				return err
			}
			return nil
		}),
		chromedp.Evaluate(`(() => {
			try {
				const c = document.createElement('canvas');
				const gl = c.getContext('webgl') || c.getContext('experimental-webgl');
				if (!gl) return JSON.stringify({webgl: false});
				const ext = gl.getExtension('WEBGL_debug_renderer_info');
				const vendor = ext ? gl.getParameter(ext.UNMASKED_VENDOR_WEBGL) : gl.getParameter(gl.VENDOR);
				const renderer = ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER);
				return JSON.stringify({webgl: true, vendor: String(vendor), renderer: String(renderer)});
			} catch (e) {
				return JSON.stringify({webgl: false, err: String(e).slice(0,80)});
			}
		})()`, &probe),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return probe
}

// TestWebGLFlagMatrix (#101 stage 3 diagnostics): which flag combination
// gives headless Chromium in a container a working WebGL context whose
// vendor/renderer the stealth layer can then override coherently.
//
// NOTE: purely diagnostic harness — it prints results via t.Logf and has NO
// assertions. Gated behind WEBGL_DIAG=1, so it never runs in CI; kept as the
// evidence base for the "flags cannot fix WebGL on Alpine/aarch64" conclusion.
func TestWebGLFlagMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("WEBGL_DIAG") == "" {
		t.Skip("set WEBGL_DIAG=1 to run the WebGL flag matrix probe")
	}
	profile := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")

	cases := []struct {
		name  string
		flags []chromedp.ExecAllocatorOption
	}{
		{"current_prod", []chromedp.ExecAllocatorOption{chromedp.DisableGPU}}, // what browser.go does when DisableGPU=true (prod default)
		{"no_disable_gpu", nil},
		{"swiftshader", []chromedp.ExecAllocatorOption{
			chromedp.Flag("use-gl", "swiftshader"),
			chromedp.Flag("enable-unsafe-swiftshader", true),
		}},
		{"angle_sw", []chromedp.ExecAllocatorOption{
			chromedp.Flag("use-angle", "swiftshader"),
			chromedp.Flag("enable-unsafe-swiftshader", true),
		}},
		{"ignore_blocklist", []chromedp.ExecAllocatorOption{
			chromedp.Flag("ignore-gpu-blocklist", true),
			chromedp.Flag("enable-unsafe-swiftshader", true),
		}},
	}
	for _, c := range cases {
		out := probeWebGL(t, c.flags, profile)
		t.Logf("%-16s -> %s", c.name, out)
		if c.name == "current_prod" && strings.Contains(out, `"webgl":false`) {
			t.Logf("NOTE: prod config indeed has NO webgl — the sannysoft finding reproduces")
		}
	}
}
