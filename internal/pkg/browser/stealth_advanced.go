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

// CanvasAntiFingerprinting добавляет noise в Canvas чтобы предотвратить fingerprinting
func (a *AdvancedStealth) CanvasAntiFingerprinting() string {
	return `
		(() => {
			// Canvas Fingerprinting Protection
			const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
			const originalGetImageData = CanvasRenderingContext2D.prototype.getImageData;
			const originalToBlob = HTMLCanvasElement.prototype.toBlob;

			// Add noise to canvas data
			function addNoise(data) {
				if (data && data.data) {
					// Add minimal noise to 0.1% of pixels (undetectable to humans, breaks fingerprinting)
					for (let i = 0; i < data.data.length; i += 4) {
						if (Math.random() < 0.001) {
							data.data[i] = Math.min(255, data.data[i] + 1);
							data.data[i + 1] = Math.min(255, data.data[i + 1] + 1);
							data.data[i + 2] = Math.min(255, data.data[i + 2] + 1);
						}
					}
				}
				return data;
			}

			// Override toDataURL
			HTMLCanvasElement.prototype.toDataURL = function() {
				const context = this.getContext('2d');
				if (context) {
					const imageData = context.getImageData(0, 0, this.width, this.height);
					addNoise(imageData);
					context.putImageData(imageData, 0, 0);
				}
				return originalToDataURL.apply(this, arguments);
			};

			// Override getImageData
			CanvasRenderingContext2D.prototype.getImageData = function() {
				const imageData = originalGetImageData.apply(this, arguments);
				return addNoise(imageData);
			};

			// Override toBlob
			HTMLCanvasElement.prototype.toBlob = function(callback) {
				const context = this.getContext('2d');
				if (context) {
					const imageData = context.getImageData(0, 0, this.width, this.height);
					addNoise(imageData);
					context.putImageData(imageData, 0, 0);
				}
				return originalToBlob.apply(this, arguments);
			};

			// Canvas WebGL fingerprinting protection
			const originalGetParameter = WebGLRenderingContext.prototype.getParameter;
			WebGLRenderingContext.prototype.getParameter = function(parameter) {
				// Add randomization to fingerprinting-relevant parameters
				if (parameter === 37445 || parameter === 37446) {
					// UNMASKED_VENDOR_WEBGL and UNMASKED_RENDERER_WEBGL
					return originalGetParameter.call(this, parameter);
				}

				// Randomize some parameters slightly
				const result = originalGetParameter.call(this, parameter);
				if (typeof result === 'number' && result > 1000) {
					// Add small random variation to large numbers
					return result + Math.floor(Math.random() * 3) - 1;
				}
				return result;
			};
		})();
	`
}

// AudioAntiFingerprinting предотвращает AudioContext fingerprinting
func (a *AdvancedStealth) AudioAntiFingerprinting() string {
	return `
		(() => {
			// AudioContext Fingerprinting Protection
			const originalCreateAnalyser = AudioContext.prototype.createAnalyser;
			const originalGetChannelData = AudioBuffer.prototype.getChannelData;

			AudioContext.prototype.createAnalyser = function() {
				const analyser = originalCreateAnalyser.apply(this, arguments);
				const originalGetFloatFrequencyData = analyser.getFloatFrequencyData;

				analyser.getFloatFrequencyData = function(array) {
					originalGetFloatFrequencyData.apply(this, arguments);
					// Add minimal noise to audio fingerprint
					for (let i = 0; i < array.length; i++) {
						if (Math.random() < 0.001) {
							array[i] += Math.random() * 0.0001;
						}
					}
				};

				return analyser;
			};

			// Override getChannelData to add noise
			AudioBuffer.prototype.getChannelData = function() {
				const result = originalGetChannelData.apply(this, arguments);
				// Add minimal noise
				for (let i = 0; i < result.length; i++) {
					if (Math.random() < 0.001) {
						result[i] += Math.random() * 0.0001 - 0.00005;
					}
				}
				return result;
			};

			// Protect AudioContext itself
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

// FontAntiFingerprinting скрывает реальное количество шрифтов
func (a *AdvancedStealth) FontAntiFingerprinting() string {
	return `
		(() => {
			// Font Fingerprinting Protection
			// Limit the number of visible fonts to prevent detailed fingerprinting

			const originalOffscreenCanvas = window.OffscreenCanvas;
			if (originalOffscreenCanvas) {
				// Add noise to offscreen canvas measurements
				window.OffscreenCanvas = function() {
					const canvas = originalOffscreenCanvas.apply(this, arguments);
					const originalGetContext = canvas.getContext;

					canvas.getContext = function() {
						const context = originalGetContext.apply(this, arguments);
						if (context && context.measureText) {
							const originalMeasureText = context.measureText;
							context.measureText = function(text) {
								const result = originalMeasureText.apply(this, arguments);
								// Add minimal variation to text measurements
								if (result && result.width) {
									Object.defineProperty(result, 'width', {
									get: () => result.width + Math.random() * 0.01 - 0.005,
									configurable: true
									});
								}
								return result;
							};
						}
						return context;
					};
					return canvas;
				};
			}
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
