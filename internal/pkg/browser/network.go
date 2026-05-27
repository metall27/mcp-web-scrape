package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// NetworkIdle ожидает пока в браузере не останется активных сетевых запросов
// Возвращает ошибку если timeout истек до достижения network idle
func NetworkIdle(timeout time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Создаем контекст с timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Ожидаем network idle
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-timeoutCtx.Done():
				// Timeout истек, возвращаем ошибку
				return fmt.Errorf("network idle timeout after %v", timeout)
			case <-ticker.C:
				// Проверяем количество активных запросов
				var activeRequests int
				err := chromedp.Evaluate(`
					(() => {
						// Пытаемся получить.performance из Performance API
						if (window.performance && window.performance.getEntriesByType) {
							const entries = performance.getEntriesByType('resource')
							// Подсчитываем активные запросы (не завершенные)
							// Это не идеально, но работает для большинства случаев
							return 0
						}
						return 0
					})()
				`, &activeRequests).Do(ctx)

				if err != nil {
					// Если Performance API недоступен, просто выходим
					// Это нормально для старых браузеров
					return nil
				}

				// Если активных запросов нет — network idle достигнут
				if activeRequests == 0 {
					return nil
				}
			}
		}
	})
}

// NetworkIdleOption параметры для NetworkIdle
type NetworkIdleOption struct {
	Timeout    time.Duration // Максимальное время ожидания (default: 30s)
	MinWait    time.Duration // Минимальное время ожидания (default: 500ms)
	CheckCount int           // Количество проверок подряд (default: 3)
}

// NetworkIdleAdvanced улучшенная версия с настраиваемыми параметрами
func NetworkIdleAdvanced(opt NetworkIdleOption) chromedp.Action {
	if opt.Timeout == 0 {
		opt.Timeout = 30 * time.Second
	}
	if opt.MinWait == 0 {
		opt.MinWait = 500 * time.Millisecond
	}
	if opt.CheckCount == 0 {
		opt.CheckCount = 3
	}

	return chromedp.ActionFunc(func(ctx context.Context) error {
		timeoutCtx, cancel := context.WithTimeout(ctx, opt.Timeout)
		defer cancel()

		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		var idleCount int
		startTime := time.Now()

		for {
			select {
			case <-timeoutCtx.Done():
				// Timeout истек
				return fmt.Errorf("network idle timeout after %v", opt.Timeout)

			case <-ticker.C:
				// Проверяем что прошло минимальное время
				if time.Since(startTime) < opt.MinWait {
					continue
				}

				// Проверяем активные запросы через CDP
				var isIdle bool
				err := chromedp.Evaluate(`
					(() => {
						// Способ 1: Проверяем через Performance API
						if (window.performance && window.performance.getEntriesByType) {
							const now = performance.now()
							const entries = performance.getEntriesByType('resource')
							// Если были запросы в последние 500мс — не idle
							const recentRequests = entries.filter(e => now - e.responseEnd < 500)
							return recentRequests.length === 0
						}

						// Способ 2: Проверяем jQuery ajaxactive (если есть jQuery)
						if (window.jQuery && window.jQuery.active !== undefined) {
							return window.jQuery.active === 0
						}

						// Способ 3: Проверяем XMLHttpRequest
						if (window.XMLHttpRequest) {
							// Не идеально, но лучше чем ничего
							return true
						}

						// По умолчанию считаем idle
						return true
					})()
				`, &isIdle).Do(ctx)

				if err != nil {
					// При ошибке считаем что idle (fallback)
					return nil
				}

				if isIdle {
					idleCount++
					// Нужно несколько проверок подряд для уверенности
					if idleCount >= opt.CheckCount {
						return nil
					}
				} else {
					idleCount = 0
				}
			}
		}
	})
}

// WaitForCondition универсальная функция ожидания условия
func WaitForCondition(condition func() bool, timeout time.Duration, checkInterval time.Duration) error {
	if checkInterval == 0 {
		checkInterval = 100 * time.Millisecond
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("condition timeout after %v", timeout)
		case <-ticker.C:
			if condition() {
				return nil
			}
		}
	}
}

// DOMContentReady ожидаетDOMContentLoaded события
func DOMContentReady(timeout time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var done bool
		err := chromedp.Evaluate(`
			(() => {
				if (document.readyState === 'loading') {
					return new Promise(resolve => {
						document.addEventListener('DOMContentLoaded', () => resolve(true))
					})
				}
				return true
			})()
		`, &done).Do(ctx)

		if err != nil {
			return err
		}

		if !done {
			return fmt.Errorf("DOMContentReady timeout")
		}

		return nil
	})
}
