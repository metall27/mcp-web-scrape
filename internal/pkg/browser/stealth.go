package browser

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/chromedp/chromedp"
)

// StealthConfig настройки для stealth режима
type StealthConfig struct {
	RandomDelay      bool          // Рандомные задержки между действиями
	MinDelay         time.Duration // Минимальная задержка
	MaxDelay         time.Duration // Максимальная задержка
	EmulateScroll    bool          // Эмуляция скролла
	ScrollSteps      int           // Количество шагов скролла
	MouseMovement    bool          // Эмуляция движений мыши
	RandomViewport   bool          // Рандомный viewport
	RandomUserAgent  bool          // Рандомный UA (уже есть отдельно)
}

// DefaultStealthConfig дефолтные настройки
var DefaultStealthConfig = StealthConfig{
	RandomDelay:    true,
	MinDelay:       100 * time.Millisecond,
	MaxDelay:       500 * time.Millisecond,
	EmulateScroll:  true,
	ScrollSteps:    3,
	MouseMovement:  true,
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

		// Рандомная задержка между MinDelay и MaxDelay
		delay := s.config.MinDelay + time.Duration(s.rnd.Int63n(int64(s.config.MaxDelay-s.config.MinDelay)))

		// Иногда добавляем небольшую дополнительную задержку (10% шанс)
		if s.rnd.Float32() < 0.1 {
			delay += time.Duration(s.rnd.Int63n(int64(200 * time.Millisecond)))
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

		// Разбиваем скролл на несколько шагов
		scrollStep := pageHeight / int64(s.config.ScrollSteps)
		if scrollStep < 100 {
			scrollStep = 100 // Минимальный шаг
		}

		// Скроллим по шагам с задержками
		for i := 0; i < s.config.ScrollSteps; i++ {
			scrollPos := int64((i + 1)) * scrollStep

			// Выполняем скролл
			err := chromedp.ActionFunc(func(ctx context.Context) error {
				return chromedp.Evaluate(fmt.Sprintf(`
					(() => {
						window.scrollTo({
							top: %d,
							behavior: 'smooth'
						});
					})()
				`, scrollPos), nil).Do(ctx)
			}).Do(ctx)

			if err != nil {
				return err
			}

			// Небольшая задержка между шагами (человеческий скролл)
			time.Sleep(100 * time.Millisecond)
		}

		// Скроллим немного вверх (люди часто возвращаются)
		err = chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`
				(() => {
					window.scrollBy({
						top: -200,
						behavior: 'smooth'
					});
				})()
			`, nil).Do(ctx)
		}).Do(ctx)

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
	width = 1200 + s.rnd.Intn(1400)  // 1200-2600
	height = 700 + s.rnd.Intn(700)   // 700-1400

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

		// После задачи: иногда скроллим
		if s.rnd.Float32() < 0.3 {
			if err := s.EmulateScroll().Do(ctx); err != nil {
				// Игнорируем ошибки скролла
			}
		}

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

// GenerateRandomFingerprint генерирует случайный fingerprint браузера
type BrowserFingerprint struct {
	ViewportWidth   int
	ViewportHeight  int
	Timezone        string
	Language        string
	Platform        string
	WebGLVendor     string
	WebGLRenderer   string
}

func (s *StealthActions) GenerateRandomFingerprint() BrowserFingerprint {
	// Timezones (популярные)
	timezones := []string{
		"America/New_York",
		"America/Los_Angeles",
		"Europe/London",
		"Europe/Berlin",
		"Europe/Paris",
		"Asia/Tokyo",
		"Australia/Sydney",
	}

	// Languages
	languages := []string{
		"en-US",
		"en-GB",
		"de-DE",
		"fr-FR",
		"ja-JP",
		"es-ES",
	}

	// Platforms
	platforms := []string{
		"Win32",
		"MacIntel",
		"Linux x86_64",
	}

	// WebGL vendors
	vendors := []string{
		"Google Inc. (NVIDIA)",
		"Google Inc. (Intel)",
		"Google Inc. (AMD)",
	}

	// Renderers
	renderers := []string{
		"ANGLE (NVIDIA GeForce GTX 1060)",
		"ANGLE (Intel(R) UHD Graphics 630)",
		"ANGLE (AMD Radeon RX 580)",
	}

	return BrowserFingerprint{
		ViewportWidth:  1920,
		ViewportHeight: 1080,
		Timezone:       timezones[s.rnd.Intn(len(timezones))],
		Language:       languages[s.rnd.Intn(len(languages))],
		Platform:       platforms[s.rnd.Intn(len(platforms))],
		WebGLVendor:    vendors[s.rnd.Intn(len(vendors))],
		WebGLRenderer:  renderers[s.rnd.Intn(len(renderers))],
	}
}
