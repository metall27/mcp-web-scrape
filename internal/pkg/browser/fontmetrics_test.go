package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestFontMockPayloadShape verifies the embedded Win32 reference decodes
// into a usable payload: families present, Ozon probe string pinned,
// span-probe table populated, fallback family sane (#107 stage C).
func TestFontMockPayloadShape(t *testing.T) {
	doc := parseFontReference()
	if doc.OS != "Win32" {
		t.Fatalf("reference os = %q, want Win32", doc.OS)
	}
	if len(doc.Fonts) < 70 {
		t.Fatalf("reference fonts = %d, want >= 70", len(doc.Fonts))
	}

	p := buildFontMockPayload(doc)
	if len(p.W) < 70 {
		t.Fatalf("payload families = %d, want >= 70", len(p.W))
	}
	if len(p.S) < 70 {
		t.Fatalf("payload span table = %d, want >= 70", len(p.S))
	}
	// The Ozon fab_chlg probe string must be pinned for Arial.
	if p.W["arial"]["r"]["mmmwwwmmmWWW"] == 0 {
		t.Fatal("arial/regular/mmmwwwmmmWWW missing from payload")
	}
	// b/i rows must be ALIVE (review #111 finding 1): the first dump
	// recorded them with a size-first font order Chromium does not apply,
	// so bold == regular for every family — dead data. At least one
	// NAMED family must show a real bold difference (58 do in the
	// current reference; generic fallbacks may legitimately match).
	liveBold, liveItalic := 0, 0
	for fam, sigs := range p.W {
		if fam == "sans-serif" || fam == "serif" || fam == "monospace" ||
			fam == "cursive" || fam == "fantasy" {
			continue
		}
		for probe, rw := range sigs["r"] {
			if bw, ok := sigs["b"][probe]; ok && bw != rw {
				liveBold++
				break
			}
		}
		for probe, rw := range sigs["r"] {
			if iw, ok := sigs["i"][probe]; ok && iw != rw {
				liveItalic++
				break
			}
		}
	}
	if liveBold == 0 {
		t.Error("no named family has bold != regular — b rows are dead (dump-tool font order regression?)")
	}
	if liveItalic == 0 {
		t.Error("no named family has italic != regular — i rows are dead (dump-tool font order regression?)")
	}
	t.Logf("live bold rows: %d, live italic rows: %d", liveBold, liveItalic)
	// Supported platforms (#112): the reference OS maps to the platform
	// list the UA selection is restricted to.
	platforms := FontMockSupportedPlatforms()
	if len(platforms) != 1 || platforms[0] != "Win32" {
		t.Errorf("FontMockSupportedPlatforms = %v, want [Win32] for the Win32 reference", platforms)
	}
	if _, ok := p.W[p.D]; !ok {
		t.Fatalf("fallback family %q not in width table", p.D)
	}

	// Linearity check: the 24px row of the reference must be ~2x the 16px
	// row (the JS mock scales linearly — validate the assumption holds for
	// an INSTALLED family; fallback families can resolve differently and
	// are not asserted).
	{
		e := doc.Fonts["Arial"]
		w16 := e.Widths["16px|mmmwwwmmmWWW"]
		w24 := e.Widths["24px|mmmwwwmmmWWW"]
		if w16 > 0 && w24 > 0 {
			ratio := w24 / w16
			if ratio < 1.48 || ratio > 1.52 {
				t.Errorf("Arial: 24px/16px ratio = %.4f, want ~1.5 (linear scaling broken)", ratio)
			}
		} else {
			t.Error("Arial 16px/24px probe rows missing from reference")
		}
	}
}

// TestFontMockScriptEmbedsPayload checks the generated JS actually
// carries the payload and has no leftover placeholder.
func TestFontMockScriptEmbedsPayload(t *testing.T) {
	js := buildFontMetricsMockScript()
	if strings.Contains(js, "__FONT_PAYLOAD__") {
		t.Fatal("placeholder not substituted")
	}
	// Payload is valid JSON.
	open := strings.Index(js, "const REF = ") + len("const REF = ")
	end := strings.Index(js[open:], ";\n")
	if end < 0 {
		t.Fatal("payload terminator not found")
	}
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(js[open:open+end]), &v); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if _, ok := v["w"]; !ok {
		t.Fatal("payload misses w table")
	}
}

// TestFontMockMeasureTextOnTargetPage is the acceptance test for stage C:
// on a real (container) Chromium with the full production stealth chain,
// measureText must answer the desktop reference numbers and two distinct
// families must produce DIFFERENT widths (the container's natural
// one-font-family collapse is exactly what the mock must hide).
func TestFontMockMeasureTextOnTargetPage(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool, err := New(Config{MaxTabs: 1, Headless: true, NoSandbox: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bctx, bcancel, err := pool.GetContext(ctx)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	defer bcancel()

	profile := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	stealth := NewStealthActions(StealthConfig{})

	var probe string
	err = chromedp.Run(bctx,
		chromedp.Navigate("about:blank"),
		stealth.InjectAntiDetectionScripts(profile),
		chromedp.Reload(),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(`(() => {
			const cv = document.createElement('canvas');
			const ctx = cv.getContext('2d');
			ctx.font = '16px Arial';
			const arial = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = 'bold 16px Arial';
			const arialBold = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '16px Verdana';
			const verdana = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '16px Tahoma';
			const tahoma = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '16px Comic Sans MS';
			const comic = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '16px Impact';
			const impact = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '24px Arial';
			const arial24 = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '16px Jokerman';
			const jokerman = ctx.measureText('mmmwwwmmmWWW').width;
			ctx.font = '16px "Segoe UI"';
			const segou = ctx.measureText('mmmwwwmmmWWW').width;
			// span probe (fab_chlg pattern)
			const span = document.createElement('span');
			span.style.cssText = 'position:absolute;left:-9999px;top:0;font-size:72px;line-height:normal;white-space:nowrap';
			document.body.appendChild(span);
			span.textContent = 'mmmwwwmmmWWW';
			const probeSpan = {};
			['br0k3nd3f4u17','Arial','Verdana','Tahoma','Comic Sans MS','Impact'].forEach(function (fam) {
				span.style.fontFamily = '"' + fam + '"';
				probeSpan[fam] = span.offsetWidth + ',' + span.offsetHeight;
			});
			span.parentNode.removeChild(span);
			// toString shape of the override
			const mtToString = String(CanvasRenderingContext2D.prototype.measureText);
			const d = Object.getOwnPropertyDescriptor(TextMetrics.prototype, 'width');
			const tmProtoWidthDesc = d ? JSON.stringify({
				e: d.enumerable, c: d.configurable,
				g: !!d.get, s: !!d.set
			}) : null;
			return JSON.stringify({
				arial: arial, arialBold: arialBold, verdana: verdana, tahoma: tahoma,
				comic: comic, impact: impact, jokerman: jokerman,
				segou: segou, arial24: arial24,
				spanArial: probeSpan['Arial'], spanVerdana: probeSpan['Verdana'],
				spanTahoma: probeSpan['Tahoma'], spanComic: probeSpan['Comic Sans MS'],
				spanImpact: probeSpan['Impact'],
				spanBaseline: probeSpan['br0k3nd3f4u17'],
				mtToString: mtToString,
				tmProtoWidthDesc: tmProtoWidthDesc
				});
		})()`, &probe),
	)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("probe: %s", probe)

	var r struct {
		Arial, ArialBold, Verdana, Tahoma, Comic, Impact, Jokerman, Segou, Arial24 float64
		SpanArial, SpanVerdana, SpanTahoma, SpanComic, SpanImpact, SpanBaseline    string
		MTToString                                                                 string
		TMProtoWidthDesc                                                           string
	}
	if err := json.Unmarshal([]byte(probe), &r); err != nil {
		t.Fatalf("parse probe: %v", err)
	}

	// Reference values from the dump (Win32 desktop Chrome 149).
	ref := parseFontReference()
	famArial := ref.Fonts["Arial"].Widths["16px|mmmwwwmmmWWW"]
	famVerdana := ref.Fonts["Verdana"].Widths["16px|mmmwwwmmmWWW"]
	famComic := ref.Fonts["Comic Sans MS"].Widths["16px|mmmwwwmmmWWW"]
	famImpact := ref.Fonts["Impact"].Widths["16px|mmmwwwmmmWWW"]
	famArial24 := ref.Fonts["Arial"].Widths["24px|mmmwwwmmmWWW"]

	// 1. Exact reference width for a pinned probe.
	if diff := r.Arial - famArial; diff > 0.6 || diff < -0.6 {
		t.Errorf("Arial/16px probe = %.3f, reference %.3f (mock not applied or drift)", r.Arial, famArial)
	}
	// 2. Linear size scaling within the mock.
	if diff := r.Arial24 - famArial24; diff > 0.6 || diff < -0.6 {
		t.Errorf("Arial/24px probe = %.3f, reference %.3f", r.Arial24, famArial24)
	}
	// 2b. Bold must answer the reference BOLD width (review #111 #1):
	// a detector setting 'bold 16px Arial' must not get the regular row.
	famArialBold := ref.Fonts["Arial"].Widths["bold 16px|mmmwwwmmmWWW"]
	if famArialBold == 0 || r.ArialBold == r.Arial {
		t.Errorf("bold row dead: mock %.3f (regular %.3f), reference bold %.3f",
			r.ArialBold, r.Arial, famArialBold)
	}
	if diff := r.ArialBold - famArialBold; diff > 0.6 || diff < -0.6 {
		t.Errorf("Arial bold probe = %.3f, reference %.3f", r.ArialBold, famArialBold)
	}
	// 3. Distinct families differ (container collapse is hidden).
	if r.Verdana == r.Tahoma {
		t.Errorf("Verdana==Tahoma (%.3f) — one-font collapse visible", r.Verdana)
	}
	if r.Comic == r.Impact {
		t.Errorf("Comic Sans==Impact (%.3f) — one-font collapse visible", r.Comic)
	}
	if r.Arial == r.Verdana {
		t.Errorf("Arial==Verdana (%.3f)", r.Arial)
	}
	// 4. Reference deltas between families are preserved.
	if diff := (r.Verdana - r.Tahoma) - (famVerdana - ref.Fonts["Tahoma"].Widths["16px|mmmwwwmmmWWW"]); diff > 0.6 || diff < -0.6 {
		t.Errorf("Verdana-Tahoma delta = %.3f, reference %.3f",
			r.Verdana-r.Tahoma, famVerdana-ref.Fonts["Tahoma"].Widths["16px|mmmwwwmmmWWW"])
	}
	if diff := (r.Comic - r.Impact) - (famComic - famImpact); diff > 0.6 || diff < -0.6 {
		t.Errorf("Comic-Impact delta = %.3f, reference %.3f",
			r.Comic-r.Impact, famComic-famImpact)
	}
	// 5. A family absent on the reference desktop (Jokerman) falls back
	// to the Windows default (Times New Roman group == baseline).
	if r.Jokerman == 0 {
		t.Error("Jokerman width is zero")
	}
	famBaseline := ref.Fonts["Times New Roman"].Widths["16px|mmmwwwmmmWWW"]
	if diff := r.Jokerman - famBaseline; diff > 0.6 || diff < -0.6 {
		t.Errorf("Jokerman should fall back to baseline %.3f, got %.3f", famBaseline, r.Jokerman)
	}
	// 6. Span probe: distinct families must be distinct, and the baseline
	// (nonexistent family) must equal the fallback group.
	if r.SpanVerdana == r.SpanTahoma || r.SpanComic == r.SpanImpact {
		t.Errorf("span probe collapse: V=%s T=%s C=%s I=%s",
			r.SpanVerdana, r.SpanTahoma, r.SpanComic, r.SpanImpact)
	}
	if r.SpanBaseline == r.SpanArial {
		t.Errorf("baseline span == Arial span (%s) — fallback mapping broken", r.SpanBaseline)
	}
	// 7. toString disguise.
	if strings.Contains(r.MTToString, "=>") || strings.Contains(r.MTToString, "function mockedMeasureText") {
		t.Errorf("measureText toString leaks override source: %s", r.MTToString)
	}
	// 8. TextMetrics.width stays a prototype getter (native shape).
	if r.TMProtoWidthDesc == "" || r.TMProtoWidthDesc == "null" {
		t.Error("TextMetrics.prototype.width descriptor missing")
	}
}

// TestFontMockPlatformGated: the Win32 reference mock is included in the
// combined script ONLY for Win32 profiles (a MacIntel profile answering
// with Windows font metrics would be a new contradiction).
func TestFontMockPlatformGated(t *testing.T) {
	sa := NewStealthActions(StealthConfig{})
	win := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	mac := NewProfile("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	if win.Platform != "Win32" || mac.Platform != "MacIntel" {
		t.Fatalf("platform parse: win=%q mac=%q", win.Platform, mac.Platform)
	}

	winScript := sa.BuildCombinedStealthScript(win)
	macScript := sa.BuildCombinedStealthScript(mac)

	fontMarker := "famTable"
	if !strings.Contains(winScript, fontMarker) {
		t.Error("font mock missing from Win32 combined script")
	}
	if strings.Contains(macScript, fontMarker) {
		t.Error("font mock present in MacIntel combined script (must be gated)")
	}
}
