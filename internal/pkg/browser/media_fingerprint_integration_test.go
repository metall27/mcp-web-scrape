package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Integration test (live Chrome) for #107 stage 3: deterministic per-profile
// media fingerprints. Asserts the three properties the Ozon challenge relies
// on:
//
//  1. STABILITY: toDataURL of the same canvas returns the same data URL on
//     every call (the challenge hashes it with crc32 — a real machine is
//     stable; Math.random noise was the detector).
//  2. NON-DEFAULT: the canvas hash differs from the raw (un-noised) render
//     and differs across profiles (two "machines" must not collide).
//  3. AUDIO: the OfflineAudioContext buffer is non-zero and stable across
//     calls (the container's bare audio stack yields all-zero output — a
//     shape no real desktop produces).
func TestDeterministicMediaFingerprint(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool, err := New(Config{MaxTabs: 1, Headless: true, NoSandbox: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	bctx, bcancel, err := pool.GetContext(ctx)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	defer bcancel()

	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	profile := NewProfile(ua)
	stealth := NewStealthActions(StealthConfig{})

	// The probe draws a fingerprint-style canvas (gradient + text + arc),
	// hashes it via the same crc32 the challenge uses, and samples the
	// offline-audio buffer — all twice, plus a raw (pre-override snapshot
	// taken through the original getImageData) hash for comparison.
	const probe = `(() => {
		const crc32 = (bytes) => {
			let c, table = [];
			for (let n = 0; n < 256; n++) {
				c = n;
				for (let k = 0; k < 8; k++) c = c & 1 ? 0xEDB88320 ^ (c >>> 1) : c >>> 1;
				table[n] = c >>> 0;
			}
			let crc = 0xFFFFFFFF;
			for (let i = 0; i < bytes.length; i++) crc = table[(crc ^ bytes[i]) & 0xFF] ^ (crc >>> 8);
			return (crc ^ 0xFFFFFFFF) >>> 0;
		};
		const drawFp = (ctx, w, h) => {
			const g = ctx.createLinearGradient(0, 0, w, h);
			g.addColorStop(0, '#f2a');
			g.addColorStop(1, '#13d');
			ctx.fillStyle = g;
			ctx.fillRect(0, 0, w, h);
			ctx.fillStyle = 'rgba(10, 84, 200, 0.7)';
			ctx.fillText('Cyrillic Ножницы @#£$', 2, 15);
			ctx.beginPath();
			ctx.arc(50, 50, 30, 0, Math.PI * 2);
			ctx.stroke();
		};
		const mkCanvas = () => {
			const c = document.createElement('canvas');
			c.width = 100; c.height = 60;
			drawFp(c.getContext('2d'), 100, 60);
			return c;
		};
		const hashCanvas = (c) => {
			const b64 = c.toDataURL('image/png');
			const bin = atob(b64.split(',')[1]);
			const arr = new Uint8Array(bin.length);
			for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
			return crc32(arr);
		};

		const c1 = mkCanvas(), c2 = mkCanvas(), c3 = mkCanvas();
		const result = {
			hash1: hashCanvas(c1),
			hash2: hashCanvas(c1),   // same canvas, second call
			hash3: hashCanvas(c2),   // fresh canvas, same drawing
			daturl_stable: c3.toDataURL() === c3.toDataURL()
		};

		// NON-DEFAULT probes (#117): a solid-black canvas has RAW rgb all
		// zero — any nonzero byte after the wrappers is an LSB flip, i.e.
		// the noise is actually applied (the old test had this check
		// locked behind an unreachable block and never ran).
		const blackCv = () => {
			const c = document.createElement('canvas');
			c.width = 100; c.height = 60;
			const x = c.getContext('2d');
			x.fillStyle = '#000';
			x.fillRect(0, 0, 100, 60);
			return c;
		};
		const countNZ = (d) => {
			// RGB only — alpha is 255 everywhere on an opaque canvas and
			// is never flipped (it would drown out the signal).
			let n = 0;
			for (let i = 0; i < d.length; i++) if (i % 4 !== 3 && d[i] !== 0) n++;
			return n;
		};
		const bl = blackCv(), blCtx = bl.getContext('2d');
		const bd1 = blCtx.getImageData(0, 0, 100, 60).data;
		const bd2 = blCtx.getImageData(0, 0, 100, 60).data;
		result.noise_pixels = countNZ(bd1);
		result.noise_stable = countNZ(bd2) === result.noise_pixels &&
			bd1[0] === bd2[0] && bd1[1] === bd2[1] && bd1[2] === bd2[2];

		// Overlapping reads must agree (#117 canvas-absolute coords):
		// pixel (50, y) read via the full window vs via a window starting
		// at sx=50 must have identical LSB decisions.
		const right = blCtx.getImageData(50, 0, 50, 60).data;
		let mism = 0;
		for (let y = 0; y < 60; y++) {
			const fi = (y * 100 + 50) * 4, ri = (y * 50) * 4;
			if (bd1[fi] !== right[ri] || bd1[fi + 1] !== right[ri + 1] ||
				bd1[fi + 2] !== right[ri + 2]) mism++;
		}
		result.overlap_mismatch = mism;

		// OffscreenCanvas hole (#117): raw black is all-zero, so nonzero
		// bytes through the Offscreen getImageData path prove the noise
		// reaches the non-inheriting prototype.
		result.off_noise_pixels = -1;
		try {
			if (typeof OffscreenCanvas !== 'undefined') {
				const oc = new OffscreenCanvas(100, 60);
				const octx = oc.getContext('2d');
				octx.fillStyle = '#000';
				octx.fillRect(0, 0, 100, 60);
				const od = octx.getImageData(0, 0, 100, 60).data;
				result.off_noise_pixels = countNZ(od);
				result.off_native_toString =
					String(OffscreenCanvasRenderingContext2D.prototype.measureText)
						.indexOf('[native code]') >= 0;
			}
		} catch (e) { result.off_noise_pixels = -2; }

		return JSON.stringify(result);
	})()`

	// Audio rendering is async and chromedp.Evaluate does not await
	// promises — kick rendering off, stash the JSON result on window, and
	// poll for it below.
	const audioKick = `(() => {
		window.__mcpwsAudio = undefined;
		try {
			const OAC = window.OfflineAudioContext || window.webkitOfflineAudioContext;
			if (!OAC) { window.__mcpwsAudio = JSON.stringify({audio: 'no-oac'}); return; }
			const oac = new OAC(1, 44100, 44100);
			const osc = oac.createOscillator();
			osc.type = 'triangle';
			osc.frequency.value = 10000;
			const comp = oac.createDynamicsCompressor();
			comp.threshold.value = -50;
			osc.connect(comp);
			comp.connect(oac.destination);
			osc.start(0);
			oac.startRendering().then((buf) => {
				const d = buf.getChannelData(0);
				let sum = 0, absmax = 0, first = d.length ? d[0] : 0;
				for (let i = 0; i < d.length; i++) {
					sum += Math.abs(d[i]);
					const a = Math.abs(d[i]);
					if (a > absmax) absmax = a;
				}
				const d2 = buf.getChannelData(0);
				window.__mcpwsAudio = JSON.stringify({
					mean: sum / d.length,
					absmax: absmax,
					first: first,
					stable: d2[0] === first && d2[100] === d[100] && d2[5000] === d[5000]
				});
			}).catch((e) => { window.__mcpwsAudio = JSON.stringify({audio: 'err:' + e.message}); });
		} catch (e) { window.__mcpwsAudio = JSON.stringify({audio: 'err:' + e.message}); }
	})()`

	var probeJSON1, probeJSON2, audioJSON string
	err = chromedp.Run(bctx,
		chromedp.Navigate("about:blank"),
		stealth.InjectAntiDetectionScripts(profile),
		chromedp.Reload(),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(probe, &probeJSON1),
		// Full reload: a NEW document must reproduce the same fingerprints.
		chromedp.Reload(),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(probe, &probeJSON2),
		chromedp.Evaluate(audioKick, nil),
		chromedp.Poll(`window.__mcpwsAudio || false`, &audioJSON, chromedp.WithPollingTimeout(20*time.Second)),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var r1, r2 map[string]interface{}
	if err := json.Unmarshal([]byte(probeJSON1), &r1); err != nil {
		t.Fatalf("probe1 not JSON: %v (%s)", err, probeJSON1)
	}
	if err := json.Unmarshal([]byte(probeJSON2), &r2); err != nil {
		t.Fatalf("probe2 not JSON: %v (%s)", err, probeJSON2)
	}
	var audio map[string]interface{}
	if err := json.Unmarshal([]byte(audioJSON), &audio); err != nil {
		t.Fatalf("audio probe not JSON: %v (%s)", err, audioJSON)
	}

	t.Logf("probe1: %s", mustJSON(r1))

	// 1. Stability: same canvas → same hash; across reloads too.
	if r1["hash1"] != r1["hash2"] {
		t.Errorf("canvas hash unstable within a document: %v != %v", r1["hash1"], r1["hash2"])
	}
	if r1["hash1"] != r2["hash1"] {
		t.Errorf("canvas hash changed across document reloads: %v != %v", r1["hash1"], r2["hash1"])
	}
	if r1["daturl_stable"] != true {
		t.Error("toDataURL not stable across calls")
	}

	// 2. NON-DEFAULT (#117): the LSB noise must actually alter pixels.
	// A 100x60 black canvas = 6000 pixels x 3 channels; at 0.4% flip
	// density the expected nonzero count is ~72 (Poisson) — require a
	// sane band so a fully-dead wrapper fails and an over-flipping one
	// fails too.
	if np, _ := r1["noise_pixels"].(float64); np < 5 || np > 600 {
		t.Errorf("canvas noise not applied or over-applied: noise_pixels=%v", np)
	}
	if r1["noise_stable"] != true {
		t.Error("partial getImageData noise not stable across reads")
	}
	// Overlapping reads must agree on shared pixels (canvas-absolute
	// coordinates): a mismatch is the #117 partial-read contradiction.
	if mm, _ := r1["overlap_mismatch"].(float64); mm != 0 {
		t.Errorf("overlapping getImageData windows disagree on %d pixels", int(mm))
	}
	// OffscreenCanvas coverage (#117): -1 = OffscreenCanvas unavailable
	// (old headless), -2 = probe threw; only a real count is meaningful.
	switch off := r1["off_noise_pixels"].(float64); {
	case off == -2:
		t.Errorf("OffscreenCanvas probe threw")
	case off >= 0 && off < 5:
		t.Errorf("OffscreenCanvas noise not applied: off_noise_pixels=%v", off)
	}
	if v, ok := r1["off_native_toString"].(bool); ok && !v {
		t.Error("OffscreenCanvas measureText toString leaks override source")
	}

	// 3. Audio: non-zero, stable.
	if a, ok := audio["audio"].(string); ok {
		t.Fatalf("audio probe failed: %v", a)
	}
	t.Logf("audio: %s", mustJSON(audio))
	if audio["stable"] != true {
		t.Errorf("audio buffer not stable across reads: %v", audio)
	}
	if absmax, _ := audio["absmax"].(float64); absmax == 0 {
		t.Error("audio buffer is all-zero (container default leaked)")
	}
	// On real hardware the absmax must be in the desktop ballpark (the
	// reference template's magnitude), not a near-zero whisper.
	if absmax, _ := audio["absmax"].(float64); absmax > 0 && absmax < 0.01 {
		t.Logf("WARNING: audio absmax %v far below desktop reference (~0.28)", absmax)
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Determinism across profiles: two different identities must not produce
// identical canvas fingerprints (otherwise the noise is constant, i.e. a
// shared tell across all our sessions).
func TestMediaFingerprintDiffersAcrossProfiles(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool, err := New(Config{MaxTabs: 1, Headless: true, NoSandbox: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	stealth := NewStealthActions(StealthConfig{})
	win := NewProfile("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	mac := NewProfile("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")

	// #118 review: crc32 of toDataURL must DIFFER across profiles — the
	// regression this guards against (a dead noise path on toDataURL)
	// made every session exfiltrate the byte-identical raw canvas, which
	// is exactly what the Ozon challenge hashes. Length alone is too
	// coarse and was only logged, never asserted.
	const hashProbe = `(() => {
		const crc32 = (bytes) => {
			let c, table = [];
			for (let n = 0; n < 256; n++) {
				c = n;
				for (let k = 0; k < 8; k++) c = c & 1 ? 0xEDB88320 ^ (c >>> 1) : c >>> 1;
				table[n] = c >>> 0;
			}
			let crc = 0xFFFFFFFF;
			for (let i = 0; i < bytes.length; i++) crc = table[(crc ^ bytes[i]) & 0xFF] ^ (crc >>> 8);
			return (crc ^ 0xFFFFFFFF) >>> 0;
		};
		const c = document.createElement('canvas');
		c.width = 100; c.height = 60;
		const ctx = c.getContext('2d');
		const g = ctx.createLinearGradient(0, 0, 100, 60);
		g.addColorStop(0, '#f2a'); g.addColorStop(1, '#13d');
		ctx.fillStyle = g; ctx.fillRect(0, 0, 100, 60);
		ctx.fillStyle = 'rgba(10, 84, 200, 0.7)';
		ctx.fillText('fp', 2, 15);
		const b64 = c.toDataURL('image/png');
		const bin = atob(b64.split(',')[1]);
		const arr = new Uint8Array(bin.length);
		for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
		return JSON.stringify({hash: crc32(arr), len: b64.length});
	})()`

	runHash := func(profile BrowserProfile) (map[string]interface{}, error) {
		bctx, bcancel, err := pool.GetContext(ctx)
		if err != nil {
			return nil, err
		}
		defer bcancel()
		var outJSON string
		err = chromedp.Run(bctx,
			chromedp.Navigate("about:blank"),
			stealth.InjectAntiDetectionScripts(profile),
			chromedp.Reload(),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.Evaluate(hashProbe, &outJSON),
		)
		if err != nil {
			return nil, err
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(outJSON), &m); err != nil {
			return nil, fmt.Errorf("hash probe not JSON: %w (%s)", err, outJSON)
		}
		return m, nil
	}

	mWin, err := runHash(win)
	if err != nil {
		t.Fatalf("win: %v", err)
	}
	mMac, err := runHash(mac)
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	nWin, _ := mWin["len"].(float64)
	nMac, _ := mMac["len"].(float64)
	hWin, _ := mWin["hash"].(float64)
	hMac, _ := mMac["hash"].(float64)
	t.Logf("toDataURL: win crc32=%v len=%v | mac crc32=%v len=%v", hWin, nWin, hMac, nMac)
	// HARD assert (#118 review): the crc32 of the noised toDataURL must
	// differ across profiles — identical hashes mean the per-pixel noise
	// is dead on the main read-out path (the NaN-origin regression).
	if hWin == hMac {
		t.Errorf("toDataURL crc32 identical across profiles (%v): noise dead on toDataURL path?", hWin)
	}
}
