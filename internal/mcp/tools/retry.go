package tools

import (
	"context"
	"errors"
	"time"
)

// RetryConfig конфигурация для retry logic
type RetryConfig struct {
	MaxAttempts  int           // Максимальное количество попыток
	InitialDelay time.Duration // Начальная задержка
	MaxDelay     time.Duration // Максимальная задержка
	Multiplier   float64       // Множитель для exponential backoff
}

// DefaultRetryConfig конфигурация по умолчанию
var DefaultRetryConfig = RetryConfig{
	MaxAttempts:  3,
	InitialDelay: 1 * time.Second,
	MaxDelay:     30 * time.Second,
	Multiplier:   2.0,
}

// RetryScraper обертка над скрапером с retry logic
type RetryScraper struct {
	scraper Scraper
	config  RetryConfig
}

// NewRetryScraper создает новый RetryScraper
func NewRetryScraper(scraper Scraper, config RetryConfig) *RetryScraper {
	if config.MaxAttempts == 0 {
		config = DefaultRetryConfig
	}
	return &RetryScraper{
		scraper: scraper,
		config:  config,
	}
}

// Scrape реализует интерфейс Scraper с retry logic
func (r *RetryScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
	var lastErr error
	delay := r.config.InitialDelay

	for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
		// Попытка скрапинга
		result, err := r.scraper.Scrape(ctx, url, opts)

		// Если успешно - возвращаем результат
		if err == nil {
			return result, nil
		}

		// Сохраняем последнюю ошибку
		lastErr = err

		// Если retry не разрешен - выходим
		var scrapeErr *ScrapeError
		if err != nil && errors.As(err, &scrapeErr) && scrapeErr != nil && !scrapeErr.CanRetry {
			return nil, err
		}

		// Если это последняя попытка - не ждем
		if attempt == r.config.MaxAttempts {
			break
		}

		// Логирование retry attempt
		// logger.Get().Info().
		// 	Int("attempt", attempt).
		// 	Int("max_attempts", r.config.MaxAttempts).
		// 	Str("delay", delay.String()).
		// 	Msg("Retrying scrape")

		// Ждем перед следующей попыткой
		select {
		case <-time.After(delay):
			// Увеличиваем задержку (exponential backoff)
			delay = time.Duration(float64(delay) * r.config.Multiplier)
			if delay > r.config.MaxDelay {
				delay = r.config.MaxDelay
			}
		case <-ctx.Done():
			// Context canceled
			return nil, &ScrapeError{
				Code:     "canceled",
				Message:  "Scrape canceled",
				Hints:    []string{},
				CanRetry: false,
			}
		}
	}

	// Все попытки провалились
	return nil, lastErr
}

// Name возвращает название оригинального скрапера
func (r *RetryScraper) Name() string {
	return r.scraper.Name()
}

// SupportsJS возвращает true если оригинальный скрапер поддерживает JS
func (r *RetryScraper) SupportsJS() bool {
	return r.scraper.SupportsJS()
}

// SupportsActions возвращает true если оригинальный скрапер поддерживает actions
func (r *RetryScraper) SupportsActions() bool {
	return r.scraper.SupportsActions()
}
