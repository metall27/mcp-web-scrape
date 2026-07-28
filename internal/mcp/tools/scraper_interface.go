package tools

import (
	"context"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// Scraper интерфейс для всех скраперов
type Scraper interface {
	// Scrape выполняет скрапинг URL
	Scrape(ctx context.Context, url string, opts Options) (*Result, error)

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
	WaitForSelector    string
	WaitForDuration    time.Duration
	WaitForNetworkIdle bool

	// Content format
	OutputFormat string // "html" или "markdown"

	// Screenshot
	Screenshot     bool
	ScreenshotMode string

	// Viewport
	ViewportWidth  int
	ViewportHeight int

	// Content blocking
	BlockImages bool

	// Stealth
	StealthEnabled bool
	StealthScroll  bool
	StealthMouse   bool

	// Proxy (не используется в HTTPScraper, только в ChromeScraper)
	ProxyEnabled bool

	// Interactive actions (только ChromeScraper)
	Actions []browser.Action

	// Named persistent session (только ChromeScraper). Когда SessionID
	// непустой, скрейпинг использует переиспользуемый browser context с
	// общим cookie jar — логин из предыдущих вызовов сохраняется.
	SessionID string

	// CloseSession=true закрывает named session после этого вызова
	// (явный cleanup). Имеет смысл только вместе с SessionID.
	CloseSession bool

	// ObserveChanges (#72): when true, after each mutating action a compact
	// page snapshot (URL, title, headings, error messages, body-changed flag,
	// text preview) is captured and returned in metadata.action_observations.
	// Lets the LLM judge whether an action had its intended effect (login
	// succeeded, content loaded, error appeared) without guessing exact text
	// to wait_for. Off by default — zero overhead on static scrapes.
	ObserveChanges bool

	// SmartSettle (#71): when true, after each mutating action (click/submit/
	// type/navigate/...) the scraper waits for the page to settle (DOM content
	// to emerge + hash to stabilize, capped ~5s) before running the next action
	// and before capturing an observation. Mirrors how a real browser/user
	// behaves — an async auth fetch or SPA route change takes time, and
	// snapshotting the instant a click returns captures an empty/stale state
	// (the root cause of issue #71: size_bytes=0 after navigate on a SPA).
	// Best-effort and non-fatal. Default ON when actions are present so the
	// tool is helpful by default; set false to force instant post-action
	// snapshots (rarely useful).
	SmartSettle bool
}

// ScrapeError подробная информация об ошибке скрапинга
type ScrapeError struct {
	Code     string   `json:"code"`            // "timeout", "blocked", "empty_response", "captcha"
	Message  string   `json:"message"`         // Человекочитаемое сообщение
	Hints    []string `json:"hints,omitempty"` // Подсказки: ["try_screenshot", "diagnostic_url"]
	CanRetry bool     `json:"can_retry"`       // Можно ли делать retry

	// Phase identifies which stage of the scrape pipeline failed (#77):
	// "navigate", "actions", "settle", "classify", "render", "fallback".
	// Empty for simple errors that don't map to a pipeline phase.
	Phase string `json:"phase,omitempty"`

	// Diagnostics is the rich structured payload for failures on pages that
	// were actually loaded (auth-required, blocked, empty after settle).
	// Nil for simple transport/HTTP errors where no page was rendered — in
	// that case Code + Message is enough. See #77 rich-diagnostics.
	Diagnostics *PageDiagnostics `json:"diagnostics,omitempty"`
}

// Error реализует error interface
func (e *ScrapeError) Error() string {
	return e.Message
}

// Result общий результат для всех скраперов (только успешные случаи)
type Result struct {
	// Content
	HTML  string
	Title string

	// Metadata
	URL         string
	FinalURL    string
	StatusCode  int
	ContentType string

	// Performance
	Duration  time.Duration
	SizeBytes int

	// Screenshot
	Screenshot []byte

	// Format info
	Format string // "html" или "markdown"

	// Actions metadata (если были actions)
	ActionsMetadata *ActionsMetadata

	// JS results (результаты execute_js действий)
	JSResults []browser.JSResult

	// Cache info
	FromCache bool

	// Method info (для unified scraper)
	Method string

	// Named session info
	SessionReused bool // true if an existing named session was reused (not newly created)

	// Fallback quality info (для httpFallback)
	// FallbackReason is set when the result came from an HTTP fallback after
	// Chrome failed. Knowing WHY Chrome failed (action_error / blocking /
	// generic) lets the caller judge whether the HTTP body is trustworthy — an
	// action error on a login-gated page means the HTTP response lacks the
	// session cookies and is almost certainly junk. See issue #49.
	FallbackReason  string
	FallbackWarning string // human-readable caveat appended to _metadata

	// Per-action results (#72): outcome of each action in the chain (status,
	// warnings, attempts). Always populated when actions were executed.
	ActionResults []browser.ActionResult

	// Page observations (#72): compact page snapshots captured after mutating
	// actions, only when ObserveChanges was requested. Empty otherwise.
	ActionObservations []browser.PageObservation

	// NetworkSummary (#77): CDP-observed network activity for this scrape —
	// real HTTP status codes, auth failures (401/403), CDN-blocking flags,
	// bounded request log. Surfaced in metadata so the LLM can diagnose
	// auth-required and blocked pages without guessing. Nil if the CDP monitor
	// failed to start (falls back to no network enrichment).
	NetworkSummary *browser.NetworkSummary

	// DOMSignals (#77): read-only page classification extracted from the DOM
	// after settle — login-form presence, SPA/framework markers, anti-bot
	// challenge hints. Passive observations surfaced in metadata to help the
	// LLM decide how to proceed; they never alter scrape flow. Nil when no
	// Chrome scrape ran (HTTP-only) or classify_page was skipped.
	DOMSignals *browser.DOMSignals
}

// PageDiagnostics is the structured failure payload attached to ScrapeError
// (and to metadata on success) so the LLM can understand WHY a page didn't
// yield the expected content and WHAT to try next. All fields optional — for
// simple transport errors only Code+Message on ScrapeError is set. #77.
type PageDiagnostics struct {
	// URL is the page URL at the point of failure/snapshot.
	URL string `json:"url,omitempty"`
	// Title is document.title at the point of failure/snapshot.
	Title string `json:"title,omitempty"`
	// HTTPStatusCode is the top-level document HTTP status (0 if unknown).
	HTTPStatusCode int `json:"http_status_code,omitempty"`
	// NetworkSummary echoes the CDP-observed network activity (nil when no
	// Chrome scrape or monitor didn't start).
	NetworkSummary *browser.NetworkSummary `json:"network_summary,omitempty"`
	// DOMSignals echoes the classify_page hints (nil when no Chrome scrape).
	DOMSignals *browser.DOMSignals `json:"dom_signals,omitempty"`
	// AttemptedSteps lists the pipeline phases that ran before the failure:
	// e.g. ["navigate", "actions:type#0", "settle", "classify"]. Helps the
	// LLM reconstruct what the tool tried.
	AttemptedSteps []string `json:"attempted_steps,omitempty"`
	// Hypothesis is a best-effort causal explanation for the failure with
	// supporting evidence. Qualitative (no numeric confidence — that was
	// explicitly rejected in #77 as opaque). Empty on success.
	Hypothesis *Hypothesis `json:"hypothesis,omitempty"`
	// Suggestions are concrete next actions the LLM can take, not generic
	// "try again". Empty on success or when no good suggestion exists.
	Suggestions []Suggestion `json:"suggestions,omitempty"`
}

// Hypothesis is a qualitative causal explanation backed by evidence strings.
type Hypothesis struct {
	Cause    string   `json:"cause"`              // e.g. "auth_required", "blocked_by_cdn", "spa_not_hydrated"
	Detail   string   `json:"detail,omitempty"`   // one-sentence human explanation
	Evidence []string `json:"evidence,omitempty"` // supporting facts: ["POST /api/auth → 401", "has_login_form: true"]
}

// Suggestion is a concrete actionable next step for the LLM.
type Suggestion struct {
	Action string `json:"action"`           // "provide_credentials", "retry_with_session", "use_execute_js"
	Detail string `json:"detail,omitempty"` // specifics, e.g. "session_id=rebrain, login form selector=..."
}

// ActionsMetadata метаданные о выполненных действиях
type ActionsMetadata struct {
	Count int
	Types []string
}
