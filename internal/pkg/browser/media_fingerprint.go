package browser

import (
	_ "embed"
	"encoding/json"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
)

//go:embed audio_reference.json
var audioReferenceRaw []byte

// audioRefDoc mirrors the desktop dump-tool output: a real
// OfflineAudioContext(1,44100,44100) triangle-10kHz→compressor(-50) render
// captured on desktop hardware (#107 stage 3). The container's audio stack
// produces an all-zero buffer — a shape no real desktop shows.
type audioRefDoc struct {
	Length    int       `json:"length"`
	AbsMax    float64   `json:"absmax"`
	Mean      float64   `json:"mean"`
	AttackZ   int       `json:"attack_zeros"`
	Stationed []float64 `json:"stationary_decimated"`
	MidSlice  []float64 `json:"mid_slice"`
}

var (
	audioRefOnce sync.Once
	audioRef     *audioRefDoc
)

// parseAudioReference decodes the embedded desktop reference (once).
func parseAudioReference() *audioRefDoc {
	audioRefOnce.Do(func() {
		var doc audioRefDoc
		if err := json.Unmarshal(audioReferenceRaw, &doc); err != nil {
			audioRef = &audioRefDoc{}
			return
		}
		audioRef = &doc
	})
	return audioRef
}

// Stage 3 of #107: deterministic per-profile media fingerprints.
//
// The Ozon challenge (script_v47_4) hashes canvas toDataURL output (crc32)
// and samples OfflineAudioContext buffers. The old stealth added noise via
// Math.random() — a NEW fingerprint on every visit. Anti-bot scoring treats
// "unique every time" as its own detection signal (a real browser on real
// hardware is stable across visits), so the old noise was actively harmful.
//
// Everything here is derived from a per-profile SEED: a pure function
// hash(seed, x, y, channel) decides which pixels get an LSB flip. Same
// profile → same canvas hash → same audio buffer on every visit and after
// every restart: for named sessions the pinned fingerprint survives
// restarts thanks to the #114 persistence, so the seed is stable too.
//
// Wrappers are disguised through the global Function.prototype.toString
// interceptor installed by the identity script (ordering contract: this
// script is appended AFTER buildIdentityHardeningScript — same as the
// font-metrics mock).

// profileSeed derives a stable seed from the pinned identity. Inputs are
// the fields that make up the session's advertised identity; changing the
// identity (new session, rotated UA) intentionally changes the media
// fingerprint with it — like moving to a different physical machine.
func profileSeed(profile BrowserProfile) uint64 {
	h := fnv.New64a()
	for _, s := range []string{
		profile.UserAgent,
		profile.Platform,
		profile.Timezone,
		profile.Language,
		profile.WebGLVendor,
		profile.WebGLRenderer,
		strconv.Itoa(profile.ScreenWidth) + "x" + strconv.Itoa(profile.ScreenHeight),
	} {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0}) // field separator
	}
	// Keep the seed inside the exact float64 integer range (2^52) so the
	// JS literal round-trips losslessly.
	return h.Sum64() & ((1 << 52) - 1)
}

// buildDeterministicMediaScript renders the stage 3 JS. Placeholder splice
// (not Sprintf — the JS body carries % chars).
func buildDeterministicMediaScript(profile BrowserProfile) string {
	const js = `
	(() => {
		const SEED = __MEDIA_SEED__;
		const AUDIO_REF = __AUDIO_REF__;

		// disguise() from the identity script (runs before this one).
		const disguiseFn = (function () {
			try { return globalThis[Symbol.for('mcpwsDisguise')]; } catch (e) { return null; }
		})() || function () {};

		// ---- deterministic PRNG core -------------------------------------
		// hash(): PURE function of (x, y, channel) — no state, so the same
		// pixel always maps to the same decision. Idempotent noise: stable
		// canvas hash across calls, visits and restarts.
		function hash(x, y, c) {
			// 32-bit mix (murmur3 finalizer) over three lanes.
			let h = SEED ^ 0x9e3779b9;
			h = Math.imul(h ^ (x | 0), 0x85ebca6b);
			h = Math.imul(h ^ (y | 0), 0xc2b2ae35);
			h = Math.imul(h ^ (c | 0), 0x27d4eb2f);
			h ^= h >>> 15; h = Math.imul(h, 0x2545f491); h ^= h >>> 13;
			return (h >>> 0) / 4294967296; // [0, 1)
		}
		// lcg(): seeded stream for audio jitter (real codec paths correlate
		// neighboring samples, so a stream is the plausible shape there).
		let lcgState = ((SEED ^ 0x1234abcd) >>> 0) || 1;
		function lcg() {
			lcgState = (Math.imul(lcgState, 1664525) + 1013904223) >>> 0;
			return lcgState / 4294967296;
		}

		const MAX_PIXELS = 4194304; // 2048² guard against huge canvases

		function noisyCopy(src, w, h) {
			const out = new Uint8ClampedArray(src);
			for (let y = 0; y < h; y++) {
				for (let x = 0; x < w; x++) {
					const i = (y * w + x) * 4;
					// LSB flips on RGB only: fingerprint canvases are
					// opaque, and alpha speckle is visible to trivial
					// pixel-diff checks.
					if (hash(x, y, 0) < 0.004) out[i] ^= 1;
					if (hash(x, y, 1) < 0.004) out[i + 1] ^= 1;
					if (hash(x, y, 2) < 0.004) out[i + 2] ^= 1;
				}
			}
			return out;
		}

		// origGetImageData must be captured BEFORE the getImageData wrapper
		// below is installed — internal noise application uses the raw
		// pixels, never the already-noised copy (double noise would cancel
		// out: the flips are deterministic XORs).
		let origGetImageData = null;
		try {
			origGetImageData = CanvasRenderingContext2D.prototype.getImageData;
		} catch (e) {}

		// ---- canvas: stable LSB noise on read-out probes -----------------
		// The canvas bitmap itself is only TEMPORARILY swapped: read → put
		// noised copy → call the original → restore. The page's own drawing
		// is not corrupted, and repeated toDataURL of the same canvas
		// yields the same data URL — the crc32 stability the challenge
		// expects from a real machine.
		function withNoisedCanvas(canvas, fn) {
			let saved = null, ctx = null;
			try {
				ctx = canvas.getContext('2d');
				if (ctx && origGetImageData && canvas.width > 0 && canvas.height > 0 &&
					canvas.width * canvas.height <= MAX_PIXELS) {
					const img = origGetImageData.call(ctx, 0, 0, canvas.width, canvas.height);
					saved = new Uint8ClampedArray(img.data);
					img.data.set(noisyCopy(img.data, canvas.width, canvas.height));
					ctx.putImageData(img, 0, 0);
				}
			} catch (e) { /* tainted canvas etc. */ }
			try {
				return fn();
			} finally {
				try {
					if (ctx && origGetImageData && saved && canvas.width > 0 && canvas.height > 0) {
						const img2 = origGetImageData.call(ctx, 0, 0, canvas.width, canvas.height);
						img2.data.set(saved);
						ctx.putImageData(img2, 0, 0);
					}
				} catch (e) {}
			}
		}

		try {
			const origToDataURL = HTMLCanvasElement.prototype.toDataURL;
			HTMLCanvasElement.prototype.toDataURL = function () {
				return withNoisedCanvas(this, function () {
					return origToDataURL.apply(this, arguments);
				}.bind(this));
			};
			disguiseFn(HTMLCanvasElement.prototype.toDataURL,
				'function toDataURL() { [native code] }');
		} catch (e) {}

		try {
			const origToBlob = HTMLCanvasElement.prototype.toBlob;
			// toBlob encodes from the bitmap captured synchronously at call
			// time (only the I/O is async), so the swap/restore pattern is
			// safe here too.
			HTMLCanvasElement.prototype.toBlob = function (callback) {
				return withNoisedCanvas(this, function () {
					return origToBlob.call(this, callback,
						arguments.length > 1 ? arguments[1] : undefined,
						arguments.length > 2 ? arguments[2] : undefined);
				});
			};
			disguiseFn(HTMLCanvasElement.prototype.toBlob,
				'function toBlob() { [native code] }');
		} catch (e) {}

		try {
			if (origGetImageData) {
				CanvasRenderingContext2D.prototype.getImageData = function (sx, sy, w, h) {
					const img = origGetImageData.call(this, sx, sy, w, h);
					if (w > 0 && h > 0 && w * h <= MAX_PIXELS) {
						img.data.set(noisyCopy(img.data, w, h));
					}
					return img;
				};
				disguiseFn(CanvasRenderingContext2D.prototype.getImageData,
					'function getImageData() { [native code] }');
			}
		} catch (e) {}

		// ---- audio: desktop-reference template, per-profile-stable --------
		// The container's audio stack yields all-zero OfflineAudioContext
		// output — a shape no real desktop produces. Two paths:
		//  - silent buffer → fill from the DESKTOP REFERENCE TEMPLATE
		//    (audio_reference.json, captured on real hardware): zero
		//    attack prefix, then the stationary oscillator/compressor
		//    waveform resampled to the buffer length, amplitudes scaled
		//    to the reference absmax, with deterministic per-profile
		//    jitter (lcg stream seeded from the profile seed);
		//  - non-silent (real stack on dev machines) → keep the shape, add
		//    only the deterministic micro-jitter.
		try {
			const origGetChannelData = AudioBuffer.prototype.getChannelData;
			const noised = new WeakSet();
			AudioBuffer.prototype.getChannelData = function (channel) {
				const data = origGetChannelData.call(this, channel);
				if (noised.has(data)) return data; // idempotent
				try { noised.add(data); } catch (e) { return data; }
				let nonzero = false;
				for (let i = 0; i < data.length; i++) {
					if (data[i] !== 0) { nonzero = true; break; }
				}
				if (!nonzero && AUDIO_REF && AUDIO_REF.st && AUDIO_REF.st.length > 1) {
					// Reference points are the stationary waveform decimated
					// from a 44100-sample render; resample to this buffer.
					const st = AUDIO_REF.st;
					const attack = Math.min(AUDIO_REF.az || 0, data.length);
					const scale = AUDIO_REF.mx || 0.28;
					for (let i = 0; i < attack; i++) data[i] = 0;
					for (let i = attack; i < data.length; i++) {
						const t = (i - attack) / (data.length - attack);
						const pos = t * (st.length - 1);
						const i0 = Math.floor(pos), i1 = Math.min(i0 + 1, st.length - 1);
						const frac = pos - i0;
						// Linear interpolation between reference points,
						// then per-profile deterministic jitter.
						const v = st[i0] * (1 - frac) + st[i1] * frac;
						data[i] = v * (1 + (lcg() - 0.5) * 0.01);
					}
				} else if (nonzero) {
					for (let i = 0; i < data.length; i++) {
						data[i] += (lcg() - 0.5) * 1e-7;
					}
				}
				return data;
			};
			disguiseFn(AudioBuffer.prototype.getChannelData,
				'function getChannelData() { [native code] }');
		} catch (e) {}

		// Analyser frequency data: pure per-index noise (stable per call
		// pattern), replacing the retired non-deterministic instability.
		try {
			const origCreateAnalyser = AudioContext.prototype.createAnalyser;
			AudioContext.prototype.createAnalyser = function () {
				const analyser = origCreateAnalyser.apply(this, arguments);
				const origGetFloat = analyser.getFloatFrequencyData.bind(analyser);
				analyser.getFloatFrequencyData = function (array) {
					origGetFloat(array);
					for (let i = 0; i < array.length; i++) {
						array[i] += (hash(i, 0, 7) - 0.5) * 1e-4;
					}
				};
				return analyser;
			};
			disguiseFn(AudioContext.prototype.createAnalyser,
				'function createAnalyser() { [native code] }');
		} catch (e) {}
	})();
	`
	// Splice the profile seed and the compact desktop audio reference
	// ({"st": stationary points, "az": attack zeros, "mx": absmax}).
	ref := parseAudioReference()
	refJSON := `{"st":[],"az":0,"mx":0}`
	if ref != nil && len(ref.Stationed) > 1 {
		b, err := json.Marshal(map[string]interface{}{
			"st": ref.Stationed,
			"az": ref.AttackZ,
			"mx": ref.AbsMax,
		})
		if err == nil {
			refJSON = string(b)
		}
	}
	out := strings.Replace(js, "__MEDIA_SEED__",
		strconv.FormatUint(profileSeed(profile), 10), 1)
	return strings.Replace(out, "__AUDIO_REF__", refJSON, 1)
}
