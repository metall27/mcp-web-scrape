package browser

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// Stage C of #107: mock CanvasRenderingContext2D.measureText and the
// span offsetWidth/offsetHeight font probe with the widths of a REAL
// desktop Chrome, captured by docs/tools/desktop-font-dump.html into
// fontmetrics_win.json (Win32, CfT 149.0.7827.155 — PR #110).
//
// Why: the container resolves nearly every family to the same handful of
// Alpine fonts, so a measureText/offsetWidth sweep reads "one font family"
// — an instant headless tell (the Ozon fab_chlg font probe). Stage 1
// (#108) installed metric-COMPATIBLE fonts so real rendering is close;
// this mock pins the EXACT reference numbers for the known probe strings
// and calibrated estimates for everything else.
//
// FontFaceSet.check is deliberately NOT mocked: the reference dump shows
// document.fonts.check returning true for all 81 families (Chrome counts
// fallback resolution as available) and the container behaves the same —
// natural parity, nothing to patch.

// FontMockSupportedPlatforms lists the navigator.platform values whose
// font metrics are covered by an embedded reference dump — i.e. the
// profiles the stage C mock can honestly serve. UA selection on the
// Chrome path is restricted to these platforms (#112): a randomly drawn
// MacIntel profile would ship the container's collapsed font metrics
// (the very tell stage C exists to hide). When a macOS reference
// (fontmetrics_mac.json) is added, "MacIntel" joins automatically via
// this function — no scraper changes needed.
func FontMockSupportedPlatforms() []string {
	doc := parseFontReference()
	switch doc.OS {
	case "Win32":
		return []string{"Win32"}
	case "MacIntel":
		return []string{"MacIntel"}
	default:
		// Unknown/failed reference: restrict nothing (old behavior).
		return nil
	}
}

//go:embed fontmetrics_win.json
var fontMetricsWinRaw []byte

// fontRefDoc mirrors the dump-tool output format.
type fontRefDoc struct {
	OS        string                  `json:"os"`
	UA        string                  `json:"ua"`
	Fonts     map[string]fontRefEntry `json:"fonts"`
	SpanProbe map[string]string       `json:"spanProbe"`
}

type fontRefEntry struct {
	Check  bool               `json:"check"`
	Widths map[string]float64 `json:"widths"`
}

// fontMockPayload is the compact structure embedded into the injected JS.
// W: family(lowercase) -> styleSig(r|b|i) -> probe string -> width at 16px.
// S: family -> [width, height] of the span probe at 72px.
// D: the fallback family key a missing family resolves to (Windows
// default font — matches the reference baseline group).
type fontMockPayload struct {
	W map[string]map[string]map[string]float64 `json:"w"`
	S map[string][2]float64                    `json:"s"`
	D string                                   `json:"d"`
}

var (
	fontPayloadOnce sync.Once
	fontPayloadStr  string
	fontRefCache    *fontRefDoc
)

// parseFontReference decodes the embedded reference dump.
func parseFontReference() *fontRefDoc {
	if fontRefCache != nil {
		return fontRefCache
	}
	var doc fontRefDoc
	if err := json.Unmarshal(fontMetricsWinRaw, &doc); err != nil {
		return &fontRefDoc{}
	}
	fontRefCache = &doc
	return fontRefCache
}

// buildFontMockPayload converts the dump into the compact JS payload.
// Only the 16px entries are kept — widths scale linearly with font-size,
// so the JS mocks other sizes by multiplication (validated against the
// reference's own 12px/24px rows in the unit test).
func buildFontMockPayload(doc *fontRefDoc) *fontMockPayload {
	p := &fontMockPayload{
		W: map[string]map[string]map[string]float64{},
		S: map[string][2]float64{},
		D: "times new roman",
	}
	for fam, entry := range doc.Fonts {
		key := strings.ToLower(strings.TrimSpace(fam))
		if key == "" {
			continue
		}
		for k, w := range entry.Widths {
			pipe := strings.Index(k, "|")
			if pipe < 0 {
				continue
			}
			style, probe := k[:pipe], k[pipe+1:]
			lstyle := strings.ToLower(style)
			// Keep only 16px rows (widths scale linearly with size —
			// validated against the reference's own 12/24px rows).
			// Accept both the canonical modifier order ('bold 16px',
			// the current dump tool) and the legacy size-first order
			// ('16px bold', the first dump whose b/i rows were dead).
			if !strings.Contains(lstyle, "16px") || strings.Contains(lstyle, "12px") || strings.Contains(lstyle, "24px") {
				continue
			}
			var sig string
			switch {
			case strings.Contains(lstyle, "bold"):
				sig = "b"
			case strings.Contains(lstyle, "italic"):
				sig = "i"
			default:
				sig = "r"
			}
			if p.W[key] == nil {
				p.W[key] = map[string]map[string]float64{}
			}
			if p.W[key][sig] == nil {
				p.W[key][sig] = map[string]float64{}
			}
			p.W[key][sig][probe] = w
		}
	}
	for fam, v := range doc.SpanProbe {
		key := strings.ToLower(strings.TrimSpace(fam))
		parts := strings.Split(v, ",")
		if len(parts) != 2 {
			continue
		}
		w, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		h, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		p.S[key] = [2]float64{w, h}
	}
	// The fallback family must exist in the width table; the reference
	// baseline group equals Times New Roman on a Win32 desktop.
	if _, ok := p.W[p.D]; !ok {
		for k := range p.W {
			p.D = k
			break
		}
	}
	return p
}

// fontMockPayloadJSON returns the serialized payload (cached).
func fontMockPayloadJSON() string {
	fontPayloadOnce.Do(func() {
		p := buildFontMockPayload(parseFontReference())
		b, err := json.Marshal(p)
		if err != nil {
			fontPayloadStr = "{}"
			return
		}
		fontPayloadStr = string(b)
	})
	return fontPayloadStr
}

// buildFontMetricsMockScript renders the stage C JS. The payload is
// spliced in via a placeholder (not Sprintf — the JS body carries % chars).
//
// Ordering contract: this script is appended AFTER the identity hardening
// script in the combined registration, so the global
// Function.prototype.toString interceptor exists and disguise() can be
// fetched through the Symbol.for registry.
func buildFontMetricsMockScript() string {
	js := `
	(() => {
		const REF = __FONT_PAYLOAD__;
		const famTable = REF.w || {};
		const spanTable = REF.s || {};
		const FALLBACK = REF.d || 'times new roman';
		const FOX = 'The quick brown fox jumps over the lazy dog';

		// disguise() from the identity script (runs before this one);
		// without it the overrides would read as JS source code.
		const disguiseFn = (function () {
			try { return globalThis[Symbol.for('mcpwsDisguise')]; } catch (e) { return null; }
		})() || function () {};

		function normFamily(f) {
			return String(f || '').replace(/["']/g, '').trim().toLowerCase();
		}

		// Resolve the first family of a CSS font list that exists in the
		// reference; unknown families fall back to the Windows default
		// (exactly what a real desktop does for a missing font).
		function lookupFam(rest) {
			const parts = String(rest || '').split(',');
			for (let i = 0; i < parts.length; i++) {
				const f = normFamily(parts[i]);
				if (!f) continue;
				if (famTable[f]) return f;
			}
			return FALLBACK;
		}

		// Parse the canvas font shorthand: "<style> <weight> <size>px <families>".
		function parseFont(fontStr) {
			const m = /(\d+(?:\.\d+)?)px\s+(.*)$/.exec(fontStr || '');
			if (!m) return null;
			const size = parseFloat(m[1]);
			const idx = String(fontStr).indexOf(m[0]);
			const prefix = String(fontStr).slice(0, idx).toLowerCase();
			let bold = prefix.indexOf('bold') >= 0;
			const wm = /(?:^|\s)(\d{3})(?:\s|$)/.exec(prefix);
			if (wm) {
				const w = parseInt(wm[1], 10);
				if (w >= 600) bold = true;
			}
			const italic = prefix.indexOf('italic') >= 0 || prefix.indexOf('oblique') >= 0;
			return { size: size, bold: bold, italic: italic, rest: m[2] };
		}

		function pickSig(pf, fam) {
			let sig = 'r';
			if (pf.bold) sig = 'b';
			else if (pf.italic) sig = 'i';
			const t = famTable[fam];
			if (!t || !t[sig]) sig = 'r';
			return sig;
		}

		function probeWidth(fam, sig, text) {
			const t = famTable[fam] && famTable[fam][sig];
			if (!t || !Object.prototype.hasOwnProperty.call(t, text)) return null;
			return t[text];
		}

		function avgChar(fam, sig) {
			const t = famTable[fam] && famTable[fam][sig];
			if (t && t[FOX]) return t[FOX] / FOX.length;
			return 7.5;
		}

		// Calibrated ROUGH estimate for strings outside the reference
		// probe set (review PR #111 finding 2): per-character classes
		// scaled by the family's average char width. Calibrated for
		// proportional Latin text — on monospace families the class
		// spread is wrong by up to +60%, so those take the exact
		// len(text) * avgChar form instead (it is exact for monospace).
		function isMono(fam, sig) {
			const t = famTable[fam] && famTable[fam][sig];
			if (!t) return false;
			// Monospace signature: single-char probes in the pinned set
			// (if any) share one width; cheaper heuristic — the classic
			// monospace families list (validated against the reference:
			// Consolas/Courier New/NSimSim measure equal-width).
			return fam === 'consolas' || fam === 'courier new' || fam === 'courier' ||
				fam === 'lucida console' || fam === 'monospace' || fam === 'andale mono' ||
				fam === 'cascadia code' || fam === 'cascadia mono' || fam === 'nsimsun' ||
				fam === 'simsun' || fam === 'mingliu' || fam === 'ms ui gothic' ||
				fam === 'menlo' || fam === 'monaco';
		}
		function estWidth(ac, text, mono) {
			if (mono) return text.length * ac;
			let w = 0;
			for (let i = 0; i < text.length; i++) {
				const ch = text.charAt(i);
				if (ch === ' ') w += ac * 0.5;
				else if ('iljtf.,;:!|'.indexOf(ch) >= 0) w += ac * 0.42;
				else if ('mwMW@'.indexOf(ch) >= 0) w += ac * 1.62;
				else if (ch >= 'A' && ch <= 'Z') w += ac * 1.28;
				else w += ac;
			}
			return w;
		}

		let origMeasure = null;
		try { origMeasure = CanvasRenderingContext2D.prototype.measureText; } catch (e) {}

		const widthCache = new Map();
		const tmVals = new WeakMap();

		function mockedMeasureText(text) {
			const t = String(text);
			const pf = parseFont(this.font);
			if (!pf) return origMeasure ? origMeasure.call(this, t) : { width: 0 };
			const key = this.font + '\x00' + t;
			let w = widthCache.get(key);
			if (w === undefined) {
				const fam = lookupFam(pf.rest);
				const sig = pickSig(pf, fam);
				const pw = probeWidth(fam, sig, t);
				if (pw !== null) w = pw * (pf.size / 16);
				else w = estWidth(avgChar(fam, sig), t, isMono(fam, sig)) * (pf.size / 16);
				if (widthCache.size > 2048) widthCache.clear();
				widthCache.set(key, w);
			}
			const m = Object.create(TextMetrics.prototype);
			tmVals.set(m, { w: w, size: pf.size });
			return m;
		}

		try {
			CanvasRenderingContext2D.prototype.measureText = mockedMeasureText;
			disguiseFn(CanvasRenderingContext2D.prototype.measureText,
				'function measureText() { [native code] }');
		} catch (e) {}

		// TextMetrics: keep the value in a WeakMap, serve it from PROTOTYPE
		// getters (a real TextMetrics instance has no own 'width' — an own
		// data prop is what a descriptor walk flags). Unknown instances
		// fall through to the native getter.
		try {
			const fields = {
				width: function (v) { return v.w; },
				actualBoundingBoxLeft: function (v) { return 0; },
				actualBoundingBoxRight: function (v) { return v.w; },
				actualBoundingBoxAscent: function (v) { return v.size * 0.8; },
				actualBoundingBoxDescent: function (v) { return v.size * 0.2; },
				fontBoundingBoxAscent: function (v) { return v.size * 1.15; },
				fontBoundingBoxDescent: function (v) { return v.size * 0.29; }
			};
			Object.keys(fields).forEach(function (name) {
				const d = Object.getOwnPropertyDescriptor(TextMetrics.prototype, name);
				const origGet = d && d.get;
				const read = fields[name];
				const get = function () {
					const v = tmVals.get(this);
					if (v) return read(v);
					return origGet ? origGet.call(this) : 0;
				};
				Object.defineProperty(TextMetrics.prototype, name, {
					get: get, set: undefined,
					enumerable: d ? d.enumerable : true,
					configurable: true
				});
				disguiseFn(get, 'function get ' + name + '() { [native code] }');
			});
		} catch (e) {}

		// ---- Span font probe (fab_chlg pattern) ----
		// The challenge measures an offscreen span (font-size:72px,
		// per-family inline fontFamily, short probe text) via
		// offsetWidth/offsetHeight. Guard is deliberately narrow — only
		// SPAN elements WITH an inline fontFamily and short text — so
		// ordinary layout reads keep their native values.
		function spanFam(el) {
			try {
				if (el.tagName !== 'SPAN') return null;
				const st = el.style;
				if (!st || !st.fontFamily) return null;
				const txt = el.textContent || '';
				if (!txt || txt.length > 64) return null;
				// The reference span was captured with line-height normal;
				// a span with an explicit line-height measures its own
				// height and must keep the native value (review #111 #3).
				if (getComputedStyle(el).lineHeight !== 'normal') return null;
				const fam = lookupFam(st.fontFamily);
				if (!spanTable[fam]) return null;
				return fam;
			} catch (e) { return null; }
		}
		function fontSizeOf(el) {
			try {
				const px = parseFloat(getComputedStyle(el).fontSize);
				if (px > 0) return px;
			} catch (e) {}
			return 72;
		}
		function patchOffsetProp(prop, idx, nativeText) {
			const d = Object.getOwnPropertyDescriptor(HTMLElement.prototype, prop);
			if (!d || !d.get) return;
			const origGet = d.get;
			const get = function () {
				try {
					const fam = spanFam(this);
					if (fam) {
						const sp = spanTable[fam];
						return Math.round(sp[idx] * fontSizeOf(this) / 72);
					}
				} catch (e) {}
				return origGet.call(this);
			};
			Object.defineProperty(HTMLElement.prototype, prop, {
				get: get, set: undefined,
				enumerable: d.enumerable, configurable: true
			});
			disguiseFn(get, nativeText);
		}
		try {
			patchOffsetProp('offsetWidth', 0, 'function get offsetWidth() { [native code] }');
			patchOffsetProp('offsetHeight', 1, 'function get offsetHeight() { [native code] }');
		} catch (e) {}
	})()
`
	return strings.Replace(js, "__FONT_PAYLOAD__", fontMockPayloadJSON(), 1)
}
