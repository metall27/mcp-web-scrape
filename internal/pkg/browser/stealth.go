package browser

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// StealthConfig настройки для stealth режима
type StealthConfig struct {
	RandomDelay     bool          // Рандомные задержки между действиями
	MinDelay        time.Duration // Минимальная задержка
	MaxDelay        time.Duration // Максимальная задержка
	EmulateScroll   bool          // Эмуляция скролла
	ScrollSteps     int           // Количество шагов скролла
	MouseMovement   bool          // Эмуляция движений мыши
	RandomViewport  bool          // Рандомный viewport
	RandomUserAgent bool          // Рандомный UA (уже есть отдельно)
}

// DefaultStealthConfig дефолтные настройки
var DefaultStealthConfig = StealthConfig{
	RandomDelay:    true,
	MinDelay:       50 * time.Millisecond,  // Balance between speed and realism
	MaxDelay:       200 * time.Millisecond, // Faster but still human-like
	EmulateScroll:  true,
	ScrollSteps:    2,     // Reduced from 3 for speed
	MouseMovement:  false, // Disabled by default (can be enabled)
	RandomViewport: false, // Выключен по умолчанию (может ломать layout)
}

type StealthActions struct {
	config StealthConfig
	rnd    *rand.Rand
}

func NewStealthActions(config StealthConfig) *StealthActions {
	if config.MinDelay == 0 {
		config.MinDelay = DefaultStealthConfig.MinDelay
	}
	if config.MaxDelay == 0 {
		config.MaxDelay = DefaultStealthConfig.MaxDelay
	}
	if config.ScrollSteps == 0 {
		config.ScrollSteps = DefaultStealthConfig.ScrollSteps
	}

	return &StealthActions{
		config: config,
		rnd:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// RandomDelay добавляет случайную задержку между действиями
func (s *StealthActions) RandomDelay() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if !s.config.RandomDelay {
			return nil
		}

		// Use configured delays (default: 50-200ms for balance)
		delay := s.config.MinDelay + time.Duration(s.rnd.Int63n(int64(s.config.MaxDelay-s.config.MinDelay)))

		// Sometimes add small additional delay (10% chance)
		if s.rnd.Float32() < 0.1 {
			delay += time.Duration(s.rnd.Int63n(int64(50 * time.Millisecond)))
		}

		time.Sleep(delay)
		return nil
	})
}

// RandomDelayInRange задержка в указанном диапазоне
func (s *StealthActions) RandomDelayInRange(min, max time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		delay := min + time.Duration(s.rnd.Int63n(int64(max-min)))
		time.Sleep(delay)
		return nil
	})
}

// EmulateScroll эмулирует скроллинг страницы как человек
func (s *StealthActions) EmulateScroll() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if !s.config.EmulateScroll {
			return nil
		}

		// Получаем высоту страницы
		var pageHeight int64
		err := chromedp.Evaluate(`
			(() => {
				return document.body.scrollHeight;
			})()
		`, &pageHeight).Do(ctx)

		if err != nil || pageHeight == 0 {
			// Если не смогли получить высоту, скроллим фиксированное количество
			pageHeight = 2000
		}

		// Use configured scroll steps (default: 2 for balance)
		scrollStep := pageHeight / int64(s.config.ScrollSteps)
		if scrollStep < 100 {
			scrollStep = 100 // Минимальный шаг
		}

		// Скроллим по шагам для реализма
		for i := 0; i < s.config.ScrollSteps; i++ {
			scrollPos := int64((i + 1)) * scrollStep

			// Используем auto (быстрее smooth) вместо instant для реализма
			err := chromedp.ActionFunc(func(ctx context.Context) error {
				return chromedp.Evaluate(fmt.Sprintf(`
					(() => {
						window.scrollTo({
							top: %d,
							behavior: 'auto'
						});
					})()
				`, scrollPos), nil).Do(ctx)
			}).Do(ctx)

			if err != nil {
				return err
			}

			// Small delay between steps for realism (50-150ms random)
			delay := 50*time.Millisecond + time.Duration(s.rnd.Int63n(int64(100*time.Millisecond)))
			time.Sleep(delay)
		}

		// Small scroll back up (humans often do this)
		err = chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`
				(() => {
					window.scrollBy({
						top: -200,
						behavior: 'auto'
					});
				})()
			`, nil).Do(ctx)
		}).Do(ctx)

		// Small pause after scroll back
		time.Sleep(50 * time.Millisecond)

		return err
	})
}

// EmulateMouseMovement эмулирует случайные движения мыши
func (s *StealthActions) EmulateMouseMovement() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if !s.config.MouseMovement {
			return nil
		}

		// Получаем размеры viewport
		var viewport map[string]int64
		err := chromedp.Evaluate(`
			(() => {
				return {
					width: window.innerWidth,
					height: window.innerHeight
				};
			})()
		`, &viewport).Do(ctx)

		if err != nil {
			return err
		}

		width := viewport["width"]
		height := viewport["height"]

		// Делаем несколько случайных движений мыши
		numMovements := 2 + s.rnd.Intn(4) // 2-5 движений

		for i := 0; i < numMovements; i++ {
			// Случайная позиция
			x := float64(s.rnd.Intn(int(width)))
			y := float64(s.rnd.Intn(int(height)))

			// Эмулируем движение мыши
			err := chromedp.MouseClickXY(x, y).Do(ctx)
			if err != nil {
				return err
			}

			// Небольшая задержка между движениями
			time.Sleep(time.Duration(50+s.rnd.Intn(150)) * time.Millisecond)
		}

		return nil
	})
}

// HumanLikeWait "человеческое" ожидание с небольшими движениями
func (s *StealthActions) HumanLikeWait(duration time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Разбиваем ожидание на части с мелкими действиями
		parts := 3 + s.rnd.Intn(3) // 3-5 частей
		partDuration := duration / time.Duration(parts)

		for i := 0; i < parts; i++ {
			time.Sleep(partDuration)

			// Иногда двигаем мышью (20% шанс)
			if s.rnd.Float32() < 0.2 {
				// Небольшое случайное движение
				offsetX := s.rnd.Intn(50) - 25 // -25 to +25
				offsetY := s.rnd.Intn(50) - 25

				// Не можем напрямую двигать мышью без chromedp,
				// но можно эмулировать через JavaScript
				err := chromedp.Evaluate(fmt.Sprintf(`
					(() => {
						window.scrollBy(%d, %d);
					})()
				`, offsetX/10, offsetY/10), nil).Do(ctx)

				if err != nil {
					// Игнорируем ошибки при мелких движениях
				}
			}
		}

		return nil
	})
}

// RandomViewport возвращает случайные размеры viewport
func (s *StealthActions) RandomViewport() (width, height int) {
	// Популярные разрешения (desktop)
	viewports := []struct {
		w, h int
	}{
		{1920, 1080}, // Full HD
		{1366, 768},  // Laptop
		{1536, 864},  // Laptop
		{1440, 900},  // MacBook
		{1280, 720},  // HD
		{2560, 1440}, // 2K
	}

	// 70% шанс использовать популярное разрешение
	if s.rnd.Float32() < 0.7 {
		idx := s.rnd.Intn(len(viewports))
		return viewports[idx].w, viewports[idx].h
	}

	// Иначе случайные размеры в разумных пределах
	width = 1200 + s.rnd.Intn(1400) // 1200-2600
	height = 700 + s.rnd.Intn(700)  // 700-1400

	return width, height
}

// ApplyStealth применяет stealth действия к задаче
func (s *StealthActions) ApplyStealth(task chromedp.Action) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Перед задачей: случайная задержка
		if err := s.RandomDelay().Do(ctx); err != nil {
			return err
		}

		// Выполняем основную задачу
		if err := task.Do(ctx); err != nil {
			return err
		}

		// УБРАНО: Не добавляем случайный скролл после каждого действия
		// Это происходит слишком часто и замедляет выполнение

		return nil
	})
}

// ApplyStealthWithScroll применяет stealth со скроллом
func (s *StealthActions) ApplyStealthWithScroll(task chromedp.Action) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Перед задачей
		if err := s.RandomDelay().Do(ctx); err != nil {
			return err
		}

		// Выполняем задачу
		if err := task.Do(ctx); err != nil {
			return err
		}

		// После задачи: скролл
		if err := s.EmulateScroll().Do(ctx); err != nil {
			// Игнорируем ошибки скролла
		}

		// Еще одна случайная задержка после скролла
		if err := s.RandomDelayInRange(50*time.Millisecond, 200*time.Millisecond).Do(ctx); err != nil {
			return err
		}

		return nil
	})
}

// BrowserFingerprint is the subset of the browser identity consumed by the
// stealth JS injection and pinned on named sessions. Since #95 Stage 1 it is
// always DERIVED from a coherent BrowserProfile (see profile.go) — never
// generated with independent random values, which produced contradictions a
// bot detector flags instantly (e.g. Windows UA + MacIntel platform).
type BrowserFingerprint struct {
	ViewportWidth  int
	ViewportHeight int
	Timezone       string
	Language       string
	Platform       string
	WebGLVendor    string
	WebGLRenderer  string
}

// InjectAntiDetectionScripts injects JavaScript to hide browser automation traces.
// This is the main entry point for Phase 3: Extended Stealth.
//
// Uses page.AddScriptToEvaluateOnNewDocument (CDP) instead of chromedp.Evaluate
// so the scripts PERSIST across navigations — chromedp.Evaluate runs on the
// current document (about:blank) and is discarded as soon as Navigate() creates
// a new document, making stealth ineffective on the target page.
//
// #95 Stage 2: the injected values come from the coherent BrowserProfile, so
// hardwareConcurrency/deviceMemory/screen are STABLE across page reloads (real
// hardware does not change per document), and the script values can never
// contradict the identity the Emulation override advertises.
func (s *StealthActions) InjectAntiDetectionScripts(profile BrowserProfile) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Build comprehensive anti-detection script
		mainScript := s.buildAntiDetectionScript(profile)

		// Advanced anti-fingerprinting methods
		advancedStealth := NewAdvancedStealth()
		advancedScript := advancedStealth.AdvancedAntiDetectionScript(profile)

		// Combine both scripts so a single registration covers everything.
		// Each script is self-contained (IIFE), so concatenation is safe.
		combinedScript := mainScript + "\n;\n" + advancedScript

		// Register via CDP so the script is re-injected on every new document,
		// surviving navigation to the target page.
		if _, err := page.AddScriptToEvaluateOnNewDocument(combinedScript).Do(ctx); err != nil {
			return fmt.Errorf("failed to register anti-detection scripts: %w", err)
		}

		return nil
	})
}

// buildAntiDetectionScript builds the comprehensive JavaScript for anti-detection
func (s *StealthActions) buildAntiDetectionScript(profile BrowserProfile) string {
	return fmt.Sprintf(`
		(() => {
			// Shared timezone helpers (#95): DST-correct values computed with
			// Intl AT CALL TIME — never cached across DST transitions of a
			// long-lived session (review: a (zone, year) cache went stale
			// after the DST switch inside a named session).
			function tzPartsFor(timeZone, date) {
				const dtf = new Intl.DateTimeFormat('en-US', {
					timeZone: timeZone, hour12: false,
					year: 'numeric', month: '2-digit', day: '2-digit',
					hour: '2-digit', minute: '2-digit', second: '2-digit'
				});
				return dtf.formatToParts(date).reduce((acc, p) => {
					if (p.type !== 'literal') acc[p.type] = p.value;
					return acc;
				}, {});
			}
			function getTimezoneOffsetForZone(timeZone, date) {
				const d = date || new Date();
				const parts = tzPartsFor(timeZone, d);
				const asUTC = Date.UTC(parts.year, parts.month - 1, parts.day,
					parts.hour === '24' ? 0 : parts.hour, parts.minute, parts.second);
				return Math.round((asUTC - d.getTime()) / 60000);
			}
			// Seasonal long zone name ("Eastern Daylight Time" vs
			// "...Standard Time") resolved via Intl for the CURRENT moment —
			// the profile value is only a fallback (review: a hardcoded
			// "Daylight" name contradicted the winter offset).
			function timezoneLongNameFor(timeZone, fallback) {
				try {
					const name = new Intl.DateTimeFormat('en-US', {
						timeZone: timeZone, timeZoneName: 'long'
					}).formatToParts(new Date()).find(p => p.type === 'timeZoneName');
					if (name && name.value) return name.value;
				} catch (e) {}
				return fallback;
			}

			// Phase 3.1: Remove navigator.webdriver
			Object.defineProperty(navigator, 'webdriver', {
				get: () => undefined,
				configurable: true
			});

			// Phase 3.2: Add fake plugins
			const fakePlugins = [
				{
					name: 'Chrome PDF Plugin',
					description: 'Portable Document Format',
					filename: 'internal-pdf-viewer',
					length: 1
				},
				{
					name: 'Chrome PDF Viewer',
					description: '',
					filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai',
					length: 1
				},
				{
					name: 'Native Client',
					description: '',
					filename: 'internal-nacl-plugin',
					length: 1
				}
			];

			Object.defineProperty(navigator, 'plugins', {
				get: () => fakePlugins,
				configurable: true
			});

			// Phase 3.3: Timezone consistency (#95: was a GETTER returning a
			// number — `+"`new Date().getTimezoneOffset()`"+` threw TypeError
			// because getTimezoneOffset became (300)(). Now a real FUNCTION.
			// The engine-level Emulation.setTimezoneOverride (applied by the
			// scraper alongside this script) makes Date/Intl report the
			// profile's zone natively; this override only guarantees the value
			// when that CDP call was unavailable.
			const targetTimezone = %q;
			const fallbackZoneName = %q;
			Date.prototype.getTimezoneOffset = function() {
				return getTimezoneOffsetForZone(targetTimezone);
			};

			// Override toString to use the human-readable zone name a real
			// Chrome prints — "GMT-0400 (Eastern Daylight Time)", never
			// "(America/New_York)" (#95 item 6). The name is resolved for
			// the current season via Intl so it can never contradict the
			// offset (review: hardcoded "Daylight" vs winter GMT-0500).
			const originalToString = Date.prototype.toString;
			Date.prototype.toString = function() {
				const zoneName = timezoneLongNameFor(targetTimezone, fallbackZoneName);
				return originalToString.call(this).replace(/(GMT[+-]\d{4}) \((.*?)\)$/, '$1 (' + zoneName + ')');
			};

			// Set locale
			Object.defineProperty(navigator, 'language', {
				get: () => %q,
				configurable: true
			});

			// Phase 3.4: WebGL fingerprint normalization
			const getParameter = WebGLRenderingContext.prototype.getParameter;
			WebGLRenderingContext.prototype.getParameter = function(parameter) {
				// UNMASKED_VENDOR_WEBGL
				if (parameter === 37445) {
					return %q;
				}
				// UNMASKED_RENDERER_WEBGL
				if (parameter === 37446) {
					return %q;
				}
				return getParameter.call(this, parameter);
			};

			// Phase 3.5: Permission API override
			if (navigator.permissions) {
				const originalQuery = navigator.permissions.query;
				navigator.permissions.query = function(permissionDesc) {
					// Return "granted" or "prompt" for common permissions
					return Promise.resolve({
						state: 'granted',
						onchange: null
					});
				};
			}

			// Additional: Override navigator.platform
			Object.defineProperty(navigator, 'platform', {
				get: () => %q,
				configurable: true
			});

			// Additional: Hide automation indicators
			// #95 Stage 3 preview: enrich the window.chrome mock with the
			// app/csi/loadTimes members real Chrome exposes.
			window.chrome = {
				runtime: {},
				app: {
					isInstalled: false,
					InstallState: { DISABLED: 'disabled', INSTALLED: 'installed', NOT_INSTALLED: 'not_installed' },
					getDetails: function() { return null; },
					getIsInstalled: function() { return false; }
				},
				csi: function() { return { onloadT: Date.now(), startE: Date.now(), pageT: Date.now() %% 100000 }; },
				loadTimes: function() {
					return {
						requestTime: Date.now() / 1000,
						startLoadTime: Date.now() / 1000,
						finishDocumentLoadTime: Date.now() / 1000,
						finishLoadTime: Date.now() / 1000,
						firstPaintTime: Date.now() / 1000,
						firstPaintAfterLoadTime: 0,
						navigationType: 'Other',
						wasFetchedViaSpdy: false,
						wasNpnNegotiated: true,
						wasAlternateProtocolAvailable: false,
						connectionReused: true,
						connectionViewId: 1
					};
				}
			};

			return true;
		})()
	`, profile.Timezone, profile.TimezoneLongName, profile.Language, profile.WebGLVendor, profile.WebGLRenderer, profile.Platform)
}
