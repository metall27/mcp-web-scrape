package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
)

// UnifiedScraper автоматический выбор лучшего метода скрапинга
type UnifiedScraper struct {
	scrapers []Scraper
	logger   zerolog.Logger
}

// NewUnifiedScraper создает новый UnifiedScraper
func NewUnifiedScraper(scrapers []Scraper) *UnifiedScraper {
	return &UnifiedScraper{
		scrapers: scrapers,
		logger:   logger.Get(),
	}
}

// Scrape автоматически выбирает лучший метод
func (s *UnifiedScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
	// 1. Определить требования
	needsJS := s.needsJavaScript(url, opts)
	needsActions := len(opts.Actions) > 0

	// 2. Выбрать скрапер
	selectedScraper := s.selectScraper(needsJS, needsActions)

	s.logger.Info().
		Str("url", url).
		Str("selected_scraper", selectedScraper.Name()).
		Bool("needs_js", needsJS).
		Bool("needs_actions", needsActions).
		Msg("Auto-selected scraper")

	// 3. Выполнить скрапинг
	result, err := selectedScraper.Scrape(ctx, url, opts)
	if err != nil {
		// 4. Fallback: попробовать следующий скрапер
		return s.tryFallback(ctx, url, opts, selectedScraper, err)
	}

	// 5. Добавить метаданные о выбранном методе
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
func (s *UnifiedScraper) tryFallback(ctx context.Context, url string, opts Options, failedScraper Scraper, originalErr error) (*Result, error) {
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
				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("all scrapers failed for %s: %w", url, originalErr)
}