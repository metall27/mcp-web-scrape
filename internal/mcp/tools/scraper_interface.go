package tools

import (
	"context"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// Scraper интерфейс для всех скраперов
type Scraper interface {
	// Scrape выполняет скрапинг URL
	Scrape(ctx context.Context, url string, opts Options) (*Result, *ScrapeError)

	// Name возвращает название скрапера
	Name() string

	// SupportsJS возвращает true если поддерживает JavaScript
	SupportsJS() bool

	// SupportsActions возвращает true если поддерживает интерактивные действия
	SupportsActions() bool
}

// Options общие опции для всех скраперов
type Options struct {
	// Timeout
	Timeout time.Duration

	// User Agent
	UserAgent string

	// Wait strategies
	WaitForSelector string
	WaitForDuration time.Duration
	WaitForNetworkIdle bool

	// Content format
	OutputFormat string // "html" или "markdown"

	// Screenshot
	Screenshot bool
	ScreenshotMode string

	// Viewport
	ViewportWidth int
	ViewportHeight int

	// Content blocking
	BlockImages bool

	// Stealth
	StealthEnabled bool
	StealthScroll bool
	StealthMouse bool

	// Proxy (не используется в HTTPScraper, только в ChromeScraper)
	ProxyEnabled bool

	// Interactive actions (только ChromeScraper)
	Actions []browser.Action
}

// ScrapeError подробная информация об ошибке скрапинга
type ScrapeError struct {
	Code     string   // "timeout", "blocked", "empty_response", "captcha"
	Message  string   // Человекочитаемое сообщение
	Hints    []string // Подсказки: ["try_screenshot", "diagnostic_url"]
	CanRetry bool     // Можно ли делать retry
}

// Error реализует error interface
func (e *ScrapeError) Error() string {
	return e.Message
}

// Result общий результат для всех скраперов (только успешные случаи)
type Result struct {
	// Content
	HTML string
	Title string

	// Metadata
	URL string
	FinalURL string
	StatusCode int
	ContentType string

	// Performance
	Duration time.Duration
	SizeBytes int

	// Screenshot
	Screenshot []byte

	// Format info
	Format string // "html" или "markdown"

	// Actions metadata (если были actions)
	ActionsMetadata *ActionsMetadata

	// Cache info
	FromCache bool

	// Method info (для unified scraper)
	Method string
}

// ActionsMetadata метаданные о выполненных действиях
type ActionsMetadata struct {
	Count int
	Types []string
}