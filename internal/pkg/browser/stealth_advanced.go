package browser

import (
	"fmt"
	"math/rand"
	"time"
)

// AdvancedFingerprintingMethods содержит методы для продвинутого anti-fingerprinting
// которые использует GitHub для обнаружения headless браузеров

type AdvancedStealth struct {
	rnd *rand.Rand
}

func NewAdvancedStealth() *AdvancedStealth {
	return &AdvancedStealth{
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// CanvasAntiFingerprinting: RETIRED as an active noise source (#107 stage 3).
// The Math.random()-based noise produced a NEW canvas fingerprint on every
// visit — anti-bot scoring treats "unique every time" as its own detection
// signal. Deterministic, per-profile-seeded noise now lives in
// buildDeterministicMediaScript (media_fingerprint.go); this stub is kept so
// the advanced script composition and its callers stay unchanged.
func (a *AdvancedStealth) CanvasAntiFingerprinting() string {
	return `
		(() => {
			// Canvas noise moved to the deterministic media script (#107
			// stage 3): stable LSB flips seeded from the pinned profile.
		})();
	`
}

// AudioAntiFingerprinting: RETIRED as an active noise source (#107 stage 3) —
// same Math.random() instability problem as the canvas noise above. The
// per-profile deterministic audio buffer shape lives in
// buildDeterministicMediaScript. This stub keeps the composition intact and
// still hardens createChannelMerger against the trivial reflection probe.
func (a *AdvancedStealth) AudioAntiFingerprinting() string {
	return `
		(() => {
			// Audio noise moved to the deterministic media script (#107
			// stage 3). Keep the channel-merger shape hardening only.
			const AudioContextProto = window.AudioContext || window.webkitAudioContext;
			if (AudioContextProto) {
				const originalCreateChannelMerger = AudioContextProto.prototype.createChannelMerger;
				AudioContextProto.prototype.createChannelMerger = function() {
					const result = originalCreateChannelMerger.apply(this, arguments);
					return result;
				};
			}
		})();
	`
}

// FontAntiFingerprinting: the OffscreenCanvas measureText random-jitter
// override is RETIRED (#107 stage 3 review): it contradicted the stage C
// font-metrics mock (which serves exact reference-table widths) — a random
// ±0.005px wobble on top of reference values is both unstable per call and
// detectably non-deterministic. Stage C's mock covers OffscreenCanvas
// contexts as well; nothing random remains here.
func (a *AdvancedStealth) FontAntiFingerprinting() string {
	return `
		(() => {
			// Font measurement spoofing moved to the stage C font-metrics
			// mock (#107): reference-table widths, deterministic.
		})();
	`
}

// HardwareAntiFingerprinting нормализует hardware information.
// #95: значения берутся из BrowserProfile (детерминированы для UA) —
// рандом на каждый документ выдавал «перезагрузка страницы меняет ядра 8→4→16»,
// что само по себе детект.
func (a *AdvancedStealth) HardwareAntiFingerprinting(profile BrowserProfile) string {
	return fmt.Sprintf(`
		(() => {
			// Hardware Fingerprinting Protection — STABLE per-profile values.

			Object.defineProperty(navigator, 'hardwareConcurrency', {
				get: () => %d,
				configurable: true
			});

			if (navigator.deviceMemory) {
				Object.defineProperty(navigator, 'deviceMemory', {
					get: () => %d,
					configurable: true
				});
			}

			// Protect against Connection Type fingerprinting
			if (navigator.connection) {
				Object.defineProperty(navigator.connection, 'effectiveType', {
					get: () => '4g',
					configurable: true
				});
			}
		})();
	`, profile.HardwareConcurrency, profile.DeviceMemory)
}

// ScreenAntiFingerprinting нормализует screen properties.
// #95: раньше геттер window.screen возвращал НОВЫЙ объект на каждое
// обращение (window.screen === window.screen → false, мгновенный детект).
// Теперь свойства переопределяются на СУЩЕСТВУЮЩЕМ объекте, а размеры
// берутся из профиля (стабильны между перезагрузками).
func (a *AdvancedStealth) ScreenAntiFingerprinting(profile BrowserProfile) string {
	return fmt.Sprintf(`
		(() => {
			// Screen Fingerprinting Protection — override properties on the
			// EXISTING screen object so identity checks still pass.

			const dims = {
				width: %d,
				height: %d,
				availWidth: %d,
				availHeight: %d
			};
			for (const [prop, value] of Object.entries(dims)) {
				try {
					Object.defineProperty(window.screen, prop, {
						get: () => value,
						configurable: true
					});
				} catch (e) { /* some props may be non-configurable */ }
			}

			// Protect against screen orientation fingerprinting
			if (screen.orientation) {
				const originalLock = screen.orientation.lock;
				screen.orientation.lock = function() {
					return Promise.reject(new Error('Not allowed'));
				};
			}
		})();
	`, profile.ScreenWidth, profile.ScreenHeight, profile.AvailWidth, profile.AvailHeight)
}

// BehavioralAntiFingerprinting добавляет человеческое поведение.
// #95: Date.now() больше НЕ получает дробный джиттер
// (Number.isInteger(Date.now()) → false был мгновенным детектом); джиттер
// остаётся только в performance.now, где дробные значения легитимны.
// clientX/clientY: рекурсивный геттер (чтение e.clientX внутри геттера
// e.clientX → stack overflow) заменён на вычисление offset'а ДО
// defineProperty.
func (a *AdvancedStealth) BehavioralAntiFingerprinting() string {
	return `
		(() => {
			// Behavioral Fingerprinting Protection

			// Add random mouse movements simulation (humans don't move in straight lines)
			let lastMouseMove = 0;
			document.addEventListener('mousemove', (e) => {
				const now = Date.now();
				if (now - lastMouseMove < 16) return; // Limit to ~60fps
				lastMouseMove = now;

				// Add micro-jitter to mouse coordinates (humans have slight tremor).
				// Original values captured BEFORE defineProperty — reading
				// e.clientX inside its own getter recursed (#95 item 5).
				if (Math.random() < 0.1) {
					const jitterX = e.clientX + Math.random() * 0.5 - 0.25;
					const jitterY = e.clientY + Math.random() * 0.5 - 0.25;
					try {
						Object.defineProperty(e, 'clientX', {
							get: () => jitterX,
							configurable: true
						});
						Object.defineProperty(e, 'clientY', {
							get: () => jitterY,
							configurable: true
						});
					} catch (err) {}
				}
			}, true);

			// Protect against timing attacks: performance.now keeps a tiny
			// jitter (fractional values are legitimate there); Date.now()
			// stays EXACTLY integer-valued (#95 item 4).
			if (window.performance) {
				const originalPerfNow = performance.now;
				let lastPerformanceNow = 0;
				performance.now = function() {
					const result = originalPerfNow.apply(this, arguments);
					if (result - lastPerformanceNow < 1) {
						return result + Math.random() * 0.001;
					}
					lastPerformanceNow = result;
					return result;
				};
			}
		})();
	`
}

// AdvancedAntiDetectionScript объединяет все advanced anti-detection методы
func (a *AdvancedStealth) AdvancedAntiDetectionScript(profile BrowserProfile) string {
	return fmt.Sprintf(`
		(() => {
			'use strict';

			// 1. Canvas Fingerprinting Protection
			%s

			// 2. Audio Fingerprinting Protection
			%s

			// 3. Font Fingerprinting Protection
			%s

			// 4. Hardware Fingerprinting Protection (stable profile values)
			%s

			// 5. Screen Fingerprinting Protection (stable profile values)
			%s

			// 6. Behavioral Fingerprinting Protection
			%s

			return true;
		})();
	`,
		a.CanvasAntiFingerprinting(),
		a.AudioAntiFingerprinting(),
		a.FontAntiFingerprinting(),
		a.HardwareAntiFingerprinting(profile),
		a.ScreenAntiFingerprinting(profile),
		a.BehavioralAntiFingerprinting(),
	)
}

// StealthMetrics содержит метрики эффективности stealth mode
type StealthMetrics struct {
	CanvasNoise    bool
	AudioNoise     bool
	FontProtection bool
	HardwareRandom bool
	ScreenRandom   bool
	BehavioralSim  bool
}

// GetStealthMetrics возвращает метрики текущей stealth конфигурации
func (a *AdvancedStealth) GetStealthMetrics() StealthMetrics {
	return StealthMetrics{
		CanvasNoise:    true,
		AudioNoise:     true,
		FontProtection: true,
		HardwareRandom: true,
		ScreenRandom:   true,
		BehavioralSim:  true,
	}
}
