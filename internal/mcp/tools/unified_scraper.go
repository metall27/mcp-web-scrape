package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/domain"
	"github.com/rs/zerolog"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
)

// UnifiedScraper автоматический выбор лучшего метода скрапинга
type UnifiedScraper struct {
	scrapers      []Scraper
	logger        zerolog.Logger
	methodLearner *domain.MethodLearner
	config        config.ScrapingConfig // Конфигурация скрапинга
}

// NewUnifiedScraper создает новый UnifiedScraper
func NewUnifiedScraper(scrapers []Scraper, methodLearner *domain.MethodLearner, cfg config.ScrapingConfig) *UnifiedScraper {
	return &UnifiedScraper{
		scrapers:      scrapers,
		logger:        logger.Get(),
		methodLearner: methodLearner,
		config:        cfg,
	}
}

// Scrape автоматически выбирает лучший метод
func (s *UnifiedScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
	// Extract domain for method learning
	domain := s.extractDomain(url)

	// 1. Определить требования
	needsJS := s.needsJavaScript(url, opts)
	needsActions := len(opts.Actions) > 0

	// 2. Проверить если есть learned preference
	var selectedScraper Scraper
	if s.methodLearner != nil && !needsActions {
		if preferredMethod, exists := s.methodLearner.GetPreferredMethod(domain); exists {
			s.logger.Info().
				Str("url", url).
				Str("domain", domain).
				Str("learned_method", preferredMethod).
				Msg("Using learned method preference")

			// Find scraper with preferred method
			selectedScraper = s.findScraperByName(preferredMethod)
		}
	}

	// 3. Если нет learned preference, использовать стандартную логику
	if selectedScraper == nil {
		selectedScraper = s.selectScraper(needsJS, needsActions)
	}

	s.logger.Info().
		Str("url", url).
		Str("selected_scraper", selectedScraper.Name()).
		Bool("needs_js", needsJS).
		Bool("needs_actions", needsActions).
		Msg("Auto-selected scraper")

	// 4. Выполнить скрапинг с возможным fast-fail timeout
	scrapeCtx := ctx

	// Определить если это первый скрапер (простой запрос без JS/actions и первый в списке)
	isFirstScraper := (!needsJS && !needsActions && len(s.scrapers) > 0 && selectedScraper.Name() == s.scrapers[0].Name())

	// Применить fast timeout для первого скрапера
	if isFirstScraper && s.config.Timeouts.FirstScraperTimeout > 0 {
		fastCtx, fastCancel := context.WithTimeout(ctx, s.config.Timeouts.FirstScraperTimeout)
		defer fastCancel()
		scrapeCtx = fastCtx

		s.logger.Debug().
			Str("timeout", s.config.Timeouts.FirstScraperTimeout.String()).
			Msg("Applied fast-fail timeout for first scraper")
	}

	result, err := selectedScraper.Scrape(scrapeCtx, url, opts)
	if err != nil {
		// Record failure
		if s.methodLearner != nil {
			s.methodLearner.RecordFailure(domain, selectedScraper.Name())
		}

		// 5. Fallback: попробовать следующий скрапер с агрессивным timeout
		fallbackCtx := ctx
		if s.config.Timeouts.FallbackTimeout > 0 {
			fallbackCtxNew, fallbackCancel := context.WithTimeout(ctx, s.config.Timeouts.FallbackTimeout)
			defer fallbackCancel()
			fallbackCtx = fallbackCtxNew

			s.logger.Debug().
				Str("timeout", s.config.Timeouts.FallbackTimeout.String()).
				Msg("Applied aggressive timeout for fallback attempt")
		}

		return s.tryFallback(fallbackCtx, url, opts, domain, selectedScraper, err)
	}

	// 6. Record success
	if s.methodLearner != nil && result != nil {
		s.methodLearner.RecordSuccess(domain, selectedScraper.Name())
	}

	// 7. Добавить метаданные о выбранном методе
	if result != nil {
		result.Method = selectedScraper.Name()
	}

	return result, nil
}

// Name возвращает название скрапера
func (s *UnifiedScraper) Name() string {
	return "Unified"
}

// SupportsJS возвращает true (если есть ChromeScraper)
func (s *UnifiedScraper) SupportsJS() bool {
	for _, scraper := range s.scrapers {
		if scraper.SupportsJS() {
			return true
		}
	}
	return false
}

// SupportsActions возвращает true (если есть ChromeScraper)
func (s *UnifiedScraper) SupportsActions() bool {
	for _, scraper := range s.scrapers {
		if scraper.SupportsActions() {
			return true
		}
	}
	return false
}

// selectScraper выбирает лучший скрапер
func (s *UnifiedScraper) selectScraper(needsJS, needsActions bool) Scraper {
	// Приоритет: Actions > JS > HTTP
	for _, scraper := range s.scrapers {
		if needsActions && scraper.SupportsActions() {
			return scraper
		}
		if needsJS && scraper.SupportsJS() {
			return scraper
		}
	}

	// Дефолт: первый скрапер (обычно HTTP)
	if len(s.scrapers) > 0 {
		return s.scrapers[0]
	}

	// Fallback: вернуть nil если нет скраперов
	return nil
}

// needsJavaScript определяет нужен ли JavaScript
func (s *UnifiedScraper) needsJavaScript(url string, opts Options) bool {
	// Явные требования через опции
	if opts.WaitForNetworkIdle {
		return true
	}

	if opts.StealthEnabled || opts.StealthScroll || opts.StealthMouse {
		return true
	}

	if len(opts.Actions) > 0 {
		return true
	}

	if opts.Screenshot || opts.ScreenshotMode == "always" {
		return true
	}

	// Известные JavaScript сайты
	jsSites := []string{
		"github.com",
		"twitter.com",
		"facebook.com",
		"react.dev",
		"vuejs.org",
		"angular.io",
		"nextjs.org",
		"stackoverflow.com",
		"reddit.com",
		"youtube.com",
		"linkedin.com",
		"instagram.com",
		"medium.com",
		"dev.to",
		"codesandbox.io",
		"replit.com",
		"figma.com",
		"notion.so",
		"trello.com",
		"slack.com",
		"discord.com",
	}

	for _, site := range jsSites {
		if strings.Contains(url, site) {
			return true
		}
	}

	return false
}

// tryFallback пробует следующий скрапер при ошибке
func (s *UnifiedScraper) tryFallback(ctx context.Context, url string, opts Options, domain string, failedScraper Scraper, originalErr error) (*Result, error) {
	for _, scraper := range s.scrapers {
		if scraper.Name() != failedScraper.Name() {
			s.logger.Warn().
				Str("url", url).
				Str("failed_scraper", failedScraper.Name()).
				Str("fallback_scraper", scraper.Name()).
				Err(originalErr).
				Msg("Trying fallback scraper")

			result, err := scraper.Scrape(ctx, url, opts)
			if err == nil {
				s.logger.Info().
					Str("url", url).
					Str("fallback_scraper", scraper.Name()).
					Msg("Fallback scraper succeeded")

				// Record success for fallback method
				if s.methodLearner != nil {
					s.methodLearner.RecordSuccess(domain, scraper.Name())
				}

				return result, nil
			} else {
				// Record failure for fallback method
				if s.methodLearner != nil {
					s.methodLearner.RecordFailure(domain, scraper.Name())
				}
			}
		}
	}

	return nil, &ScrapeError{
		Code:     "all_scrapers_failed",
		Message:  fmt.Sprintf("All scrapers failed for %s: %s", url, originalErr.Error()),
		Hints:    []string{"retry", "try_different_method"},
		CanRetry: true,
	}
}

// extractDomain извлекает домен из URL для method learning
func (s *UnifiedScraper) extractDomain(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// findScraperByName находит скрапер по имени
func (s *UnifiedScraper) findScraperByName(name string) Scraper {
	for _, scraper := range s.scrapers {
		if scraper.Name() == name {
			return scraper
		}
	}
	return nil
}