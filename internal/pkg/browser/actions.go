package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

// Action представляет одно интерактивное действие
type Action struct {
	Type     string        // Тип действия: click, type, scroll_to, wait_for, login, etc.
	Selector string        // CSS selector для элемента
	Text     string        // Текст для ввода (для type) или URL (для navigate)
	Value    string        // Значение (для select_option)
	Timeout  time.Duration // Timeout для ожидания
	Retries  int           // Количество ретраев при ошибке

	// Login composite action (#77 Tier-2). login fills the username + password
	// fields, submits, and verifies the outcome (URL left the login form / auth
	// signals appeared). Unlike a manual type→type→click chain, login returns
	// a structured LoginResult with evidence so the LLM sees WHY auth
	// succeeded or failed. All four fields below are required for type=login.
	UsernameSelector string // CSS selector for the username/email field
	Username         string // credential value for the username field
	PasswordSelector string // CSS selector for the password field
	Password         string // credential value for the password field
	// SubmitSelector is the form submit button (optional — defaults to the
	// first button[type=submit] inside the password field's <form>).
	SubmitSelector string

	// Extract structured (#77 Tier-3). The extract_structured action pulls
	// field values from the DOM by CSS selectors into a JSON object/array,
	// surfaced in metadata.extracted_data. This is the deterministic,
	// no-magic counterpart to returning raw markdown and asking the LLM to
	// parse it: the LLM supplies a field→selector mapping and the tool
	// returns clean values. See extract.go.
	//
	// ExtractSchema maps an output field name to a FieldSpec (selector +
	// attribute). When Container is empty the result is a single object
	// {field:value,...}; when set, the tool runs the schema against every
	// element matching Container and returns an array of objects — the
	// catalog/listing use case (e.g. one record per .product-card).
	ExtractSchema map[string]FieldSpec
	Container     string
}

// FieldSpec describes one field in an extract_structured schema (#77 Tier-3).
// Variant 2 of the design discussion: selector + attr only, everything comes
// back as a string. No numeric/boolean coercion — the LLM parses values in
// downstream logic, which is both simpler and avoids silent truncation of
// locale-formatted numbers ("1,299.00" → 1). attr defaults to "text"
// (element.innerText); "href"/"src"/"data-*" pull the named attribute.
type FieldSpec struct {
	Selector string `json:"selector"` // CSS selector within the container (or page when no container)
	Attr     string `json:"attr"`     // "text" (default) | attribute name: href, src, data-sku, ...
}

// JSResult хранит результат выполнения execute_js action
type JSResult struct {
	ActionIndex int         // Индекс действия в массиве actions (0-based)
	Result      interface{} // Возвращаемое значение JavaScript (или nil при undefined)
	Err         error       // Ошибка выполнения (nil при успехе)
}

// ActionResult records the outcome of a single action within a chain. Surfaced
// in the scrape response metadata so the LLM (or user) can see what happened at
// each step — not just "all actions succeeded" or "scrape failed". See #72.
type ActionResult struct {
	Index    int    `json:"index"`    // 0-based action index in the chain
	Type     string `json:"type"`     // action type (click, type, wait_for_text, ...)
	Selector string `json:"selector"` // CSS selector used (empty for text-only actions)
	Status   string `json:"status"`   // "completed", "soft_timeout", "failed"
	Warning  string `json:"warning"`  // non-empty on soft_timeout (e.g. text not found)
	Error    string `json:"error"`    // non-empty on failed
	Attempts int    `json:"attempts"` // number of attempts actually executed
}

// PageObservation is a compact snapshot of the page state captured after a
// mutating action (click/submit/type/navigate/...), when observe mode is on
// (observe_changes=true). Designed to be small (~50-150 tokens) so it can be
// included in the scrape response metadata without flooding context. Gives the
// LLM enough signal to decide whether the action had the intended effect
// (login succeeded, content loaded, error appeared) without needing to guess
// the exact text to wait for — solving the core UX problem of #72.
//
// #71 enrichment: AuthSignals (cookie count + auth-like storage keys) lets the
// LLM see that an auth flow actually took effect, even when URL/body haven't
// visibly changed within the settle window — the universal "login succeeded"
// signal for SPA sites that store tokens in cookies/localStorage.
type PageObservation struct {
	ActionIndex int      `json:"action_index"` // 0-based index of the preceding action
	URL         string   `json:"url"`          // window.location.href after the action
	URLChanged  bool     `json:"url_changed"`  // true if URL differs from previous observation
	Title       string   `json:"title"`
	Headings    []string `json:"headings"`     // up to 5 h1/h2 texts (truncated to 100 chars)
	Errors      []string `json:"errors"`       // visible error/alert messages (up to 5)
	BodyChanged bool     `json:"body_changed"` // true if body content hash changed
	TextPreview string   `json:"text_preview"` // first ~300 chars of visible body text

	// #71 auth-state enrichment. Populated when observe=true. These fields let
	// the LLM judge whether a login/auth action succeeded by inspecting the
	// actual auth state, not just URL/body changes that may lag behind async
	// token storage. AuthKeys lists localStorage keys whose name matches common
	// token/session markers (auth, token, session, user, ...); values are never
	// exposed — only key presence.
	AuthSignals *AuthSignals `json:"auth_signals,omitempty"`

	// Settle is the report from the post-action smart-settle wait (when
	// enabled): which phase ended the wait and how long it took. Lets the LLM
	// distinguish "page settled cleanly" from "still loading at timeout".
	Settle *SettleReport `json:"settle,omitempty"`
}

// AuthSignals summarises the auth-relevant browser state at observation time.
// It is deliberately value-free: only counts and key NAMES are reported, never
// token/cookie values, so credentials never leak into scrape metadata.
type AuthSignals struct {
	Cookies  int      `json:"cookies"`   // document.cookie length (chars) — rough session-cookie proxy
	AuthKeys []string `json:"auth_keys"` // localStorage keys matching token/session/auth/user markers (names only)
}

// SoftTimeoutError indicates a wait-type action (wait_for, wait_for_text) timed
// out. Unlike a hard error, this does NOT abort the action chain: it's recorded
// as a warning and execution continues. The LLM can inspect page observations
// to decide whether the wait condition was eventually met. See issue #72.
type SoftTimeoutError struct {
	ActionIndex int
	ActionType  string
	Message     string
}

func (e *SoftTimeoutError) Error() string {
	return e.Message
}

// IsSoftTimeoutError reports whether err is a *SoftTimeoutError.
func IsSoftTimeoutError(err error) bool {
	var ste *SoftTimeoutError
	return errors.As(err, &ste)
}

// ActionValidationError indicates a deterministic validation failure in a
// user-supplied action: a required field is empty, an action type is unknown,
// or the input is otherwise malformed. Such errors describe bad input, not a
// transient page condition, so the retry loop must NOT retry them — retrying a
// definitively-invalid action only wastes 3+ seconds of backoff before failing
// the same way. See issue #50.
type ActionValidationError struct {
	ActionIndex int    // 0-based index of the offending action
	ActionType  string // type of the offending action
	Reason      string // human-readable reason
}

func (e *ActionValidationError) Error() string {
	return fmt.Sprintf("action %d (%s): %s", e.ActionIndex+1, e.ActionType, e.Reason)
}

// IsActionValidationError reports whether err is an ActionValidationError.
// Used by the retry loop to skip retries on deterministic validation failures.
func IsActionValidationError(err error) bool {
	var ve *ActionValidationError
	return errors.As(err, &ve)
}

// requiredFields maps each action type to the Action fields that MUST be
// non-empty for the action to be valid. Centralised here so both ParseActions
// (eager reject before any Chrome work) and ExecuteAction (defense-in-depth)
// enforce the same rules. Adding a new action type only needs an entry here.
var requiredFields = map[string][]struct {
	field    string
	accessor func(a Action) string
}{
	"click":               {{"selector", func(a Action) string { return a.Selector }}},
	"type":                {{"selector", func(a Action) string { return a.Selector }}, {"text", func(a Action) string { return a.Text }}},
	"submit":              {{"selector", func(a Action) string { return a.Selector }}},
	"scroll_to":           {{"selector", func(a Action) string { return a.Selector }}},
	"wait_for":            {{"selector", func(a Action) string { return a.Selector }}},
	"wait_for_text":       {{"text", func(a Action) string { return a.Text }}},
	"wait_for_navigation": {}, // no required fields — waits for URL change
	"wait_for_content":    {}, // no required fields — waits for non-empty stable body
	"hover":               {{"selector", func(a Action) string { return a.Selector }}},
	"select_option":       {{"selector", func(a Action) string { return a.Selector }}, {"value", func(a Action) string { return a.Value }}},
	"execute_js":          {{"text", func(a Action) string { return a.Text }}},
	"upload_file":         {{"selector", func(a Action) string { return a.Selector }}, {"text", func(a Action) string { return a.Text }}},
	"navigate":            {{"text", func(a Action) string { return a.Text }}},
	"login": {
		{"username_selector", func(a Action) string { return a.UsernameSelector }},
		{"username", func(a Action) string { return a.Username }},
		{"password_selector", func(a Action) string { return a.PasswordSelector }},
		{"password", func(a Action) string { return a.Password }},
	},
	// extract_structured (#77 Tier-3): the schema map is required. The
	// accessor returns "1" when non-empty so the generic empty-string check
	// in validateAction treats a populated map as valid. container is
	// optional (single-record mode when omitted).
	"extract_structured": {
		{"schema", func(a Action) string {
			if len(a.ExtractSchema) > 0 {
				return "1"
			}
			return ""
		}},
	},
}

// RequiredField is the exported mirror of an entry in requiredFields: an
// action type and the names of the Action struct fields it requires.
type RequiredField struct {
	ActionType string   // e.g. "wait_for"
	Fields     []string // e.g. ["selector"]
}

// RequiredFields returns the validation rules for every action type in a form
// consumable from outside the browser package (notably the tools package's
// schema/validator consistency test). The JSON-schema advertised to LLM
// clients must encode the same required fields; see
// TestActionsSchemaMatchesValidator in internal/mcp/tools.
func RequiredFields() []RequiredField {
	out := make([]RequiredField, 0, len(requiredFields))
	for actionType, fields := range requiredFields {
		names := make([]string, len(fields))
		for i, f := range fields {
			names[i] = f.field
		}
		out = append(out, RequiredField{ActionType: actionType, Fields: names})
	}
	return out
}

// validateAction checks that action has the required fields for its type.
// Returns an *ActionValidationError (nil for valid actions) so callers in the
// retry loop can detect it via IsActionValidationError and skip retrying.
// actionIndex is the 0-based index used for error reporting.
func validateAction(action Action, actionIndex int) error {
	fields, known := requiredFields[action.Type]
	if !known {
		return &ActionValidationError{
			ActionIndex: actionIndex,
			ActionType:  action.Type,
			Reason:      fmt.Sprintf("unknown action type: %s", action.Type),
		}
	}
	for _, f := range fields {
		if f.accessor(action) == "" {
			return &ActionValidationError{
				ActionIndex: actionIndex,
				ActionType:  action.Type,
				Reason:      fmt.Sprintf("%s is required for %s action", f.field, action.Type),
			}
		}
	}
	return nil
}

type ActionExecutor struct {
	logger    zerolog.Logger
	stealth   *StealthActions
	retries   int           // Дефолтное количество ретраев
	timeout   time.Duration // Дефолтный timeout
	jsResults []JSResult    // Результаты выполнения execute_js действий

	// Per-action outcomes (#72). Populated during ExecuteActions and surfaced
	// in the scrape response metadata so the LLM sees what happened at each
	// step, not just the final success/failure.
	results      []ActionResult
	observations []PageObservation // only when observeChanges=true
	observe      bool              // whether to capture PageObservation after mutating actions

	// Baseline state for the next observation: URL + body hash captured BEFORE
	// a mutating action, so captureObservation can diff against it. Keyed by
	// action index. Cleared after the corresponding observation is captured.
	baselineURL  map[int]string
	baselineHash map[int]string

	// smartSettle: when true, after a mutating action (click/submit/type/
	// navigate/...) ExecuteActions waits for the page to settle (DOM content
	// to emerge + hash to stabilize) before running the next action and before
	// capturing an observation. This mirrors how a real browser/user behaves:
	// an async auth fetch or SPA route change takes time, and snapshotting the
	// page the instant the click returns captures an empty/stale state (the
	// root cause of issue #71 "Bug B": size_bytes=0 after navigate on a SPA).
	// Best-effort and non-fatal: a settle timeout just snapshots whatever we
	// have. See settle.go.
	smartSettle bool
	settleCfg   SettleConfig

	// lastLoginResult holds the outcome of the most recent login action (#77
	// Tier-2), surfaced in metadata so the LLM sees the auth verdict + evidence.
	// nil when no login action ran in this executor.
	lastLoginResult *LoginResult

	// lastExtractData / lastExtractReport hold the outcome of the most recent
	// extract_structured action (#77 Tier-3). Nil/empty when none ran.
	lastExtractData   interface{}
	lastExtractReport *ExtractReport
}

// GetJSResults возвращает результаты всех execute_js действий
func (e *ActionExecutor) GetJSResults() []JSResult {
	return e.jsResults
}

// GetResults возвращает результат каждого действия в цепочке (#72).
func (e *ActionExecutor) GetResults() []ActionResult {
	return e.results
}

// GetObservations возвращает наблюдения страницы после mutating actions (#72).
func (e *ActionExecutor) GetObservations() []PageObservation {
	return e.observations
}

// GetLoginResult возвращает результат последнего login action (#77 Tier-2) или
// nil, если login не выполнялся. Содержит verdict (success/ambiguous/auth_failed)
// + evidence — почему сделан такой вывод.
func (e *ActionExecutor) GetLoginResult() *LoginResult {
	return e.lastLoginResult
}

// GetExtractData возвращает извлечённые данные последнего extract_structured
// action (#77 Tier-3) или nil, если extract не выполнялся. Форма: object для
// single-режима (без container), array of objects — для container-режима.
func (e *ActionExecutor) GetExtractData() interface{} {
	return e.lastExtractData
}

// GetExtractReport возвращает отчёт последнего extract_structured action
// (#77 Tier-3) или nil. Содержит records/fields/missing/warnings — качественный
// фидбек, помогающий LLM понять, какие селекторы не сработали.
func (e *ActionExecutor) GetExtractReport() *ExtractReport {
	return e.lastExtractReport
}

// recordJSResult добавляет или перезаписывает результат execute_js по actionIndex.
// При retry предыдущая запись для того же индекса заменяется — без дубликатов.
func (e *ActionExecutor) recordJSResult(r JSResult) {
	for i, existing := range e.jsResults {
		if existing.ActionIndex == r.ActionIndex {
			e.jsResults[i] = r
			return
		}
	}
	e.jsResults = append(e.jsResults, r)
}

// recordResult upserts a per-action outcome by index (same dedup semantics as
// recordJSResult: a retried action overwrites its previous outcome).
func (e *ActionExecutor) recordResult(r ActionResult) {
	for i, existing := range e.results {
		if existing.Index == r.Index {
			e.results[i] = r
			return
		}
	}
	e.results = append(e.results, r)
}

// NewActionExecutor создает новый экземпляр ActionExecutor.
// observeChanges=true включает снятие PageObservation после mutating actions.
// smartSettle=true включает post-action settle wait (см. settle.go).
// settleCfg задаёт параметры settle (нулевое значение = DefaultSettleConfig).
func NewActionExecutor(logger zerolog.Logger, stealth *StealthActions, observeChanges, smartSettle bool, settleCfg SettleConfig) *ActionExecutor {
	if smartSettle && settleCfg.MaxWait <= 0 {
		settleCfg = DefaultSettleConfig()
	}
	return &ActionExecutor{
		logger:      logger,
		stealth:     stealth,
		retries:     3,                // Дефолт: 3 ретрая
		timeout:     30 * time.Second, // Дефолт: 30s timeout
		observe:     observeChanges,
		smartSettle: smartSettle,
		settleCfg:   settleCfg,
	}
}

// ExecuteActions выполняет список действий последовательно.
//
// Изменение #72: wait-подобные действия (wait_for, wait_for_text) возвращают
// *SoftTimeoutError при таймауте. Такая ошибка НЕ прерывает цепочку и НЕ
// является hard-fail: она записывается как warning в ActionResult, scrape
// продолжается и возвращает страницу в том состоянии, в котором оказалась.
// Ранее таймаут wait-а абортил весь scrape и LLM не видел страницу вообще —
// именно это описано в issue #72. Hard-fail остаётся для validation errors
// (#50) и для всех не-wait действий (click/type/...): их неудача означает, что
// последующие действия не имеют смысла.
func (e *ActionExecutor) ExecuteActions(ctx context.Context, actions []Action) error {
	for i, action := range actions {
		e.logger.Info().
			Int("action_number", i+1).
			Str("type", action.Type).
			Str("selector", action.Selector).
			Msg("Executing action")

		// Определяем количество ретраев для этого действия
		retries := e.retries
		if action.Retries > 0 {
			retries = action.Retries
		}

		// Определяем timeout для этого действия
		timeout := e.timeout
		if action.Timeout > 0 {
			timeout = action.Timeout
		}

		// Снимаем observation ДО mutating action, чтобы потом сравнить.
		// (observation после action снимается ниже в блоке завершения.)
		if e.observe && isMutatingAction(action.Type) {
			// Предзапоминаем URL/body-hash для сравнения "changed?".
			if err := e.captureBaseline(ctx, i); err != nil {
				e.logger.Debug().Err(err).Msg("observation baseline capture failed (non-critical)")
			}
		}

		// Выполняем действие с ретраями
		var lastErr error
		attemptMade := 0
		for attempt := 0; attempt < retries; attempt++ {
			if attempt > 0 {
				e.logger.Debug().
					Int("attempt", attempt+1).
					Int("max_retries", retries).
					Msg("Retrying action")

				// Экспоненциальная задержка между ретраями
				backoff := time.Duration(attempt) * 500 * time.Millisecond
				time.Sleep(backoff)
			}

			// Создаем контекст с timeout
			actionCtx, cancel := context.WithTimeout(ctx, timeout)

			// Выполняем действие (передаём индекс)
			err := e.ExecuteAction(actionCtx, action, i)
			cancel()
			attemptMade = attempt + 1

			if err == nil {
				e.logger.Info().
					Int("action_number", i+1).
					Str("type", action.Type).
					Int("attempt", attempt+1).
					Msg("Action completed successfully")

				lastErr = nil
				break // Успех, выходим из цикла ретраев
			}

			lastErr = err

			// Validation errors (empty selector, unknown type, ...) are
			// deterministic — retrying the same bad input always fails the
			// same way. Skip the remaining attempts to avoid wasting 3+ seconds
			// of backoff on something that can never succeed. See issue #50.
			if IsActionValidationError(err) {
				e.logger.Warn().
					Int("action_number", i+1).
					Str("type", action.Type).
					Int("attempt", attempt+1).
					Err(err).
					Msg("Action validation error — not retrying")
				break
			}

			// Soft timeout (#72): wait-condition не выполнено за timeout.
			// НЕ ретраим и НЕ прерываем цепочку — это не ошибка, а сигнал
			// «условие не наступило, но страница, возможно, уже в нужном
			// состоянии». Запишем warning и продолжим.
			if IsSoftTimeoutError(err) {
				e.logger.Info().
					Int("action_number", i+1).
					Str("type", action.Type).
					Int("attempt", attempt+1).
					Err(err).
					Msg("Wait condition timed out (soft) — continuing chain")
				break
			}

			e.logger.Warn().
				Int("action_number", i+1).
				Str("type", action.Type).
				Int("attempt", attempt+1).
				Err(err).
				Msg("Action failed")
		}

		// Классификация исхода действия для ActionResult (#72).
		e.recordActionOutcome(i, action, lastErr, attemptMade)

		// Soft timeout — продолжаем цепочку, не прерываем.
		if lastErr != nil && IsSoftTimeoutError(lastErr) {
			// observation снимем ниже единым блоком (как для успеха).
		} else if lastErr != nil {
			// Hard fail (click/type/validation) — последующие действия не
			// имеют смысла, прерываем цепочку. Возвращаемый error приведёт к
			// fallback, но ActionResult уже сохранён в executor и попадёт в
			// metadata.
			return fmt.Errorf("action %d (%s on %s) failed after %d attempts: %w",
				i+1, action.Type, action.Selector, attemptMade, lastErr)
		}

		// Smart settle (#71): после mutating action дождаться, пока SPA
		// отреагирует — body наполнится контентом и хэш стабилизируется.
		// Без этого observation и следующий action видят пустой/stale state
		// (корень "Bug B": navigate на SPA отдаёт size_bytes=0). Best-effort:
		// таймаут не ошибка, просто снимаем что есть. wait_* actions не
		// нуждаются в settle — они сами являются ожиданием.
		var settleReport *SettleReport
		if e.smartSettle && isMutatingAction(action.Type) {
			report, serr := SmartSettle(ctx, e.logger, e.settleCfg)
			if serr != nil && !errors.Is(serr, context.Canceled) {
				e.logger.Debug().Err(serr).Int("action_number", i+1).Msg("smart settle ended (non-critical)")
			}
			settleReport = report
		}

		// Observation после mutating action (#72). SettleReport передаётся,
		// чтобы LLM видел: страница застабилизировалась ("stable") или таймаут.
		if e.observe && isMutatingAction(action.Type) {
			if err := e.captureObservation(ctx, i, settleReport); err != nil {
				e.logger.Debug().Err(err).Msg("observation capture failed (non-critical)")
			}
		}

		// Небольшая задержка между действиями (если stealth включен)
		if e.stealth != nil && i < len(actions)-1 {
			if err := e.stealth.RandomDelay().Do(ctx); err != nil {
				e.logger.Debug().Err(err).Msg("Stealth delay failed (non-critical)")
			}
		}
	}

	return nil
}

// isMutatingAction reports whether an action type is expected to change page
// state (and thus warrants a PageObservation snapshot). wait_* and scroll/hover
// are read-only w.r.t. the document and are skipped to avoid noise.
func isMutatingAction(t string) bool {
	switch t {
	case "click", "submit", "type", "select_option", "upload_file", "navigate", "execute_js", "login":
		return true
	}
	return false
}

// recordActionOutcome converts the result of a single action's retry loop into
// an ActionResult and stores it via recordResult.
func (e *ActionExecutor) recordActionOutcome(index int, action Action, lastErr error, attempts int) {
	r := ActionResult{
		Index:    index,
		Type:     action.Type,
		Selector: action.Selector,
		Attempts: attempts,
	}
	switch {
	case lastErr == nil:
		r.Status = "completed"
	case IsSoftTimeoutError(lastErr):
		r.Status = "soft_timeout"
		r.Warning = lastErr.Error()
	default:
		r.Status = "failed"
		r.Error = lastErr.Error()
	}
	e.recordResult(r)
}

// captureBaseline snapshots the page URL and a body-content hash before a
// mutating action runs. captureObservation later diffs against these values to
// report url_changed / body_changed. Both are best-effort: a failure is logged
// and ignored (observation is a non-critical convenience, not a correctness
// requirement). See #72.
func (e *ActionExecutor) captureBaseline(ctx context.Context, actionIndex int) error {
	if e.baselineURL == nil {
		e.baselineURL = make(map[int]string)
		e.baselineHash = make(map[int]string)
	}

	var snapshot struct {
		URL  string `json:"url"`
		Hash string `json:"hash"`
	}
	// djb2 hash of a trimmed innerText — cheap and stable enough to detect
	// content change without sending the whole body across the CDP boundary.
	err := chromedp.Evaluate(`(() => {
		const t = (document.body && document.body.innerText) ? document.body.innerText : '';
		const trimmed = t.replace(/\s+/g, ' ').trim().slice(0, 5000);
		let h = 5381;
		for (let i = 0; i < trimmed.length; i++) {
			h = ((h << 5) + h + trimmed.charCodeAt(i)) | 0;
		}
		return { url: location.href, hash: String(h) };
	})()`, &snapshot).Do(ctx)
	if err != nil {
		return err
	}
	e.baselineURL[actionIndex] = snapshot.URL
	e.baselineHash[actionIndex] = snapshot.Hash
	return nil
}

// captureObservation runs the compact page-state probe after a mutating action
// and appends a PageObservation to the executor. url_changed / body_changed are
// computed against the baseline captured by captureBaseline for the same index.
//
// The probe is a single chromedp.Evaluate call returning URL, title, headings,
// error/alert texts, body hash, a text preview, and auth-state signals (cookie
// length + auth-like localStorage key names) — all bounded to keep the response
// small (~50-150 tokens). Designed so the LLM can judge whether a login /
// navigation / loading action had the intended effect without guessing the exact
// text to wait_for. See #72 (observations) and #71 (auth signals).
//
// settleReport (may be nil) is attached verbatim so the LLM can see whether the
// post-action smart-settle reached a stable state or timed out still loading.
func (e *ActionExecutor) captureObservation(ctx context.Context, actionIndex int, settleReport *SettleReport) error {
	var probe struct {
		URL       string   `json:"url"`
		Title     string   `json:"title"`
		Headings  []string `json:"headings"`
		Errors    []string `json:"errors"`
		Hash      string   `json:"hash"`
		Preview   string   `json:"preview"`
		CookieLen int      `json:"cookie_len"`
		AuthKeys  []string `json:"auth_keys"`
	}
	err := chromedp.Evaluate(`(() => {
		const trunc = (s, n) => s ? s.trim().slice(0, n) : '';
		const heads = Array.from(document.querySelectorAll('h1,h2')).slice(0, 5).map(h => trunc(h.innerText, 100));
		// Error messages: elements whose class/id hints at an alert/error.
		const errs = Array.from(document.querySelectorAll('[class*="error" i],[class*="alert" i],[role="alert"]'))
			.map(e => trunc(e.innerText, 120)).filter(Boolean).slice(0, 5);
		const t = (document.body && document.body.innerText) ? document.body.innerText : '';
		const trimmed = t.replace(/\s+/g, ' ').trim();
		let h = 5381;
		const forHash = trimmed.slice(0, 5000);
		for (let i = 0; i < forHash.length; i++) {
			h = ((h << 5) + h + forHash.charCodeAt(i)) | 0;
		}
		// #71 auth-state signals. We never read VALUES — only key NAMES and a
		// coarse cookie-length proxy — so tokens never leak into metadata.
		let cookieLen = 0;
		try { cookieLen = document.cookie.length; } catch(e) {}
		const authKeys = [];
		try {
			const re = /(auth|token|session|user|jwt|bearer)/i;
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				if (k && re.test(k)) authKeys.push(k);
			}
		} catch(e) {}
		return {
			url: location.href,
			title: document.title || '',
			headings: heads,
			errors: errs,
			hash: String(h),
			preview: trunc(trimmed, 300),
			cookie_len: cookieLen,
			auth_keys: authKeys
		};
	})()`, &probe).Do(ctx)
	if err != nil {
		return err
	}

	prevHash := e.baselineHash[actionIndex]
	prevURL := e.baselineURL[actionIndex]
	delete(e.baselineHash, actionIndex)
	delete(e.baselineURL, actionIndex)

	obs := PageObservation{
		ActionIndex: actionIndex,
		URL:         probe.URL,
		URLChanged:  prevURL != "" && probe.URL != prevURL,
		Title:       probe.Title,
		Headings:    probe.Headings,
		Errors:      probe.Errors,
		BodyChanged: prevHash != "" && probe.Hash != prevHash,
		TextPreview: probe.Preview,
		AuthSignals: &AuthSignals{
			Cookies:  probe.CookieLen,
			AuthKeys: probe.AuthKeys,
		},
		Settle: settleReport,
	}
	e.observations = append(e.observations, obs)
	return nil
}

// ExecuteAction выполняет одно действие (actionIndex — индекс в массиве actions)
func (e *ActionExecutor) ExecuteAction(ctx context.Context, action Action, actionIndex int) error {
	// Defense-in-depth: validateAction is also called in ParseActions, but an
	// internal caller could construct an Action directly. Reject early so the
	// retry loop sees an ActionValidationError and skips backoff. See issue #50.
	if err := validateAction(action, actionIndex); err != nil {
		return err
	}
	switch action.Type {
	case "click":
		return e.ExecuteClick(ctx, action.Selector)
	case "type":
		return e.ExecuteType(ctx, action.Selector, action.Text)
	case "submit":
		return e.ExecuteSubmit(ctx, action.Selector)
	case "scroll_to":
		return e.ExecuteScrollTo(ctx, action.Selector)
	case "wait_for":
		return e.ExecuteWaitFor(ctx, action.Selector, action.Timeout)
	case "wait_for_text":
		return e.ExecuteWaitForText(ctx, action.Text, action.Timeout)
	case "wait_for_navigation":
		return e.ExecuteWaitForNavigation(ctx, action.Timeout)
	case "wait_for_content":
		return e.ExecuteWaitForContent(ctx, action.Timeout)
	case "hover":
		return e.ExecuteHover(ctx, action.Selector)
	case "select_option":
		return e.ExecuteSelectOption(ctx, action.Selector, action.Value)
	case "execute_js":
		return e.ExecuteJS(ctx, action.Text, actionIndex)
	case "upload_file":
		return e.ExecuteUploadFile(ctx, action.Selector, action.Text)
	case "navigate":
		return e.ExecuteNavigate(ctx, action.Text)
	case "login":
		return e.ExecuteLogin(ctx, action)
	case "extract_structured":
		return e.ExecuteExtract(ctx, action)
	default:
		return &ActionValidationError{
			ActionIndex: actionIndex,
			ActionType:  action.Type,
			Reason:      fmt.Sprintf("unknown action type: %s", action.Type),
		}
	}
}

// ExecuteClick кликает по элементу
func (e *ActionExecutor) ExecuteClick(ctx context.Context, selector string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for click action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Msg("Clicking element")

	// Сначала прокручиваем к элементу (чтобы он был виден)
	scrollErr := chromedp.ScrollIntoView(selector, chromedp.ByQuery).Do(ctx)
	if scrollErr != nil {
		e.logger.Warn().Err(scrollErr).Msg("Failed to scroll to element before click")
	}

	// Кликаем по элементу
	err := chromedp.Click(selector, chromedp.ByQuery).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to click element %s: %w", selector, err)
	}

	// Небольшая задержка после клика (чтобы UI обновился)
	time.Sleep(100 * time.Millisecond)

	return nil
}

// ExecuteType вводит текст в поле
func (e *ActionExecutor) ExecuteType(ctx context.Context, selector, text string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for type action")
	}
	if text == "" {
		return fmt.Errorf("text is required for type action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Str("text_length", fmt.Sprintf("%d", len(text))).
		Msg("Typing text")

	// Ждём появления элемента на странице (с таймаутом 10с), чтобы не висеть
	// полный timeout (30с) в Focus, если элемента нет в DOM или он hidden.
	// Focus/Click в chromedp используют polling-sleeper — без WaitVisible они
	// ждут до context deadline, давая бесполезный "context deadline exceeded".
	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	err := chromedp.WaitVisible(selector, chromedp.ByQuery).Do(waitCtx)
	waitCancel()
	if err != nil {
		return fmt.Errorf("element %s not visible within 10s: %w", selector, err)
	}

	// Фокусируемся на элементе (мгновенно после WaitVisible)
	err = chromedp.Focus(selector, chromedp.ByQuery).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to focus element %s: %w", selector, err)
	}

	// Очищаем поле перед вводом
	err = chromedp.Evaluate(fmt.Sprintf(`
		(() => {
			const el = document.querySelector(%s);
			if (el) el.value = '';
		})()
	`, quoteSelector(selector)), nil).Do(ctx)
	if err != nil {
		e.logger.Warn().Err(err).Msg("Failed to clear field (non-critical)")
	}

	// Вводим текст с человеческой скоростью (если stealth включен)
	if e.stealth != nil {
		// Печатаем по символу с задержкой
		for _, char := range text {
			err := chromedp.SendKeys(selector, string(char), chromedp.ByQuery).Do(ctx)
			if err != nil {
				return fmt.Errorf("failed to type text: %w", err)
			}

			// Случайная задержка между символами (50-150ms)
			time.Sleep(50 + time.Duration(len(text)%100))
		}
	} else {
		// Вводим весь текст сразу
		err = chromedp.SendKeys(selector, text, chromedp.ByQuery).Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to type text: %w", err)
		}
	}

	return nil
}

// ExecuteSubmit отправляет форму.
//
// Стратегия: клик ПЕРВЫМ, нативный form.submit() — только как fallback.
//
// На React/Zustand/Vue SPA нативный HTMLFormElement.submit() (chromedp.Submit)
// полностью обходит synthetic onSubmit-обработчик фреймворка: не диспатчится
// React form submission, не сохраняется токен авторизации, а для форм с
// method=GET креды физически попадают в query string (issue #69). Клик по
// button[type=submit] корректно триггерит как React onClick, так и нативный
// form submission через событийную модель — это то, что делает реальный
// пользователь.
func (e *ActionExecutor) ExecuteSubmit(ctx context.Context, selector string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for submit action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Msg("Submitting form")

	// 1. Click-first: симулирует реальный клик пользователя по кнопке submit.
	// Это диспатчит synthetic onSubmit (React/Vue) и работает на SPA.
	if err := e.ExecuteClick(ctx, selector); err == nil {
		return nil
	}

	// 2. Fallback: нативный form.submit(). Помогает только на классических
	// серверных формах (без JS-обработчиков). На SPA скорее всего бесполезен,
	// но оставлен для совместимости с legacy-формами.
	e.logger.Debug().
		Str("selector", selector).
		Msg("Click failed, falling back to native form.submit()")
	if err := chromedp.Submit(selector, chromedp.ByQuery).Do(ctx); err != nil {
		return fmt.Errorf("failed to submit form %s: %w", selector, err)
	}

	return nil
}

// ExecuteScrollTo прокручивает к элементу
func (e *ActionExecutor) ExecuteScrollTo(ctx context.Context, selector string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for scroll_to action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Msg("Scrolling to element")

	// Прокручиваем к элементу
	err := chromedp.ScrollIntoView(selector, chromedp.ByQuery).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to scroll to element %s: %w", selector, err)
	}

	// Ждем немного после скролла
	time.Sleep(200 * time.Millisecond)

	return nil
}

// ExecuteWaitFor ждет появления элемента.
//
// #72: таймаут — это SoftTimeoutError, НЕ hard-fail. Цепочка действий не
// прерывается, scrape возвращает страницу в текущем состоянии.
func (e *ActionExecutor) ExecuteWaitFor(ctx context.Context, selector string, timeout time.Duration) error {
	if selector == "" {
		return fmt.Errorf("selector is required for wait_for action")
	}

	if timeout == 0 {
		timeout = e.timeout
	}

	e.logger.Debug().
		Str("selector", selector).
		Dur("timeout", timeout).
		Msg("Waiting for element")

	// Ждем элемента с timeout
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := chromedp.WaitVisible(selector, chromedp.ByQuery).Do(waitCtx)
	if err != nil {
		// #72: soft timeout — не прерываем scrape, даём LLM увидеть страницу.
		return &SoftTimeoutError{
			ActionType: "wait_for",
			Message:    fmt.Sprintf("element %s not visible within %v", selector, timeout),
		}
	}

	return nil
}

// ExecuteWaitForText ждет появления текста на странице.
//
// #72: таймаут — это SoftTimeoutError, НЕ hard-fail. Цепочка действий не
// прерывается, scrape возвращает страницу в текущем состоянии. Ранее таймаут
// wait_for_text абортил весь scrape (90с wasted, страница не возвращалась) —
// основная жалоба issue #72.
func (e *ActionExecutor) ExecuteWaitForText(ctx context.Context, text string, timeout time.Duration) error {
	if text == "" {
		return fmt.Errorf("text is required for wait_for_text action")
	}

	if timeout == 0 {
		timeout = e.timeout
	}

	e.logger.Debug().
		Str("text", text).
		Dur("timeout", timeout).
		Msg("Waiting for text")

	// Ждем текста с polling
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			// #72: soft timeout — не прерываем scrape, даём LLM увидеть страницу.
			return &SoftTimeoutError{
				ActionType: "wait_for_text",
				Message:    fmt.Sprintf("text '%s' not found within %v", text, timeout),
			}
		case <-ticker.C:
			var found bool
			err := chromedp.Evaluate(fmt.Sprintf(`
				(() => {
					return document.body.innerText.includes(%s);
				})()
			`, quoteString(text)), &found).Do(ctx)

			if err == nil && found {
				e.logger.Debug().
					Str("text", text).
					Msg("Text found")
				return nil
			}
		}
	}
}

// ExecuteWaitForNavigation ждёт, пока URL страницы изменится относительно
// значения на момент запуска action. Полезно после click/submit на SPA, чтобы
// дождаться клиентского роутинга (history API) или серверного редиректа, прежде
// чем снимать snapshot или выполнять следующее действие.
//
// Таймаут — SoftTimeoutError (#72): НЕ прерывает цепочку. Если URL не сменился
// за timeout, scrape продолжается и возвращает страницу в текущем состоянии —
// LLM видит по observation, что навигации не произошло (например, логин провалился).
//
// Замечание по SPA-роутингу: client-side route change меняет URL через history
// API без полной перезагрузки. DOM при этом может ещё не перерисоваться — для
// контента используйте wait_for_content после wait_for_navigation.
func (e *ActionExecutor) ExecuteWaitForNavigation(ctx context.Context, timeout time.Duration) error {
	if timeout == 0 {
		timeout = e.timeout
	}

	// Фиксируем исходный URL.
	var startURL string
	if err := chromedp.Location(&startURL).Do(ctx); err != nil {
		return fmt.Errorf("wait_for_navigation: failed to read initial URL: %w", err)
	}

	e.logger.Debug().
		Str("url", startURL).
		Dur("timeout", timeout).
		Msg("Waiting for navigation")

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			return &SoftTimeoutError{
				ActionType: "wait_for_navigation",
				Message:    fmt.Sprintf("URL did not change from %s within %v", startURL, timeout),
			}
		case <-ticker.C:
			var currentURL string
			if err := chromedp.Location(&currentURL).Do(ctx); err != nil {
				// Context canceled — propagate so the action loop can exit cleanly.
				return err
			}
			if currentURL != startURL {
				e.logger.Debug().
					Str("from", startURL).
					Str("to", currentURL).
					Msg("Navigation detected")
				return nil
			}
		}
	}
}

// ExecuteWaitForContent ждёт, пока body страницы не наполнится осмысленным
// контентом и его хэш не стабилизируется. Решает "Bug B" из issue #71: после
// navigate на SPA страница загружается пустым shell'ом (<body></body>), а
// React/Vue/Zustand рендерят контент асинхронно после data-fetch.
//
// По сути это явный, настраиваемый аналог smart-settle: polling body.innerText
// до превышения min length + stabilisation window. Используйте после navigate
// на SPA, либо после wait_for_navigation, когда нужно дождаться не только смены
// URL, но и отрисовки контента.
//
// Таймаут — SoftTimeoutError (#72): НЕ прерывает цепочку.
func (e *ActionExecutor) ExecuteWaitForContent(ctx context.Context, timeout time.Duration) error {
	if timeout == 0 {
		timeout = e.timeout
	}

	e.logger.Debug().
		Dur("timeout", timeout).
		Msg("Waiting for content")

	// Переиспользуем механизм settle: emergence (non-empty body) + stabilize
	// (hash stable). Кап = timeout действия.
	cfg := DefaultSettleConfig()
	cfg.MaxWait = timeout
	report, err := SmartSettle(ctx, e.logger, cfg)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("wait_for_content: %w", err)
	}
	// SmartSettle всегда возвращает nil error на таймаут (best-effort), но мы
	// хотим сообщить soft-timeout, если контент так и не появился — это даёт
	// LLM явный сигнал в ActionResult.Warning.
	if report != nil && (report.Phase == "emergence_timeout" || report.Phase == "stabilize_timeout") {
		return &SoftTimeoutError{
			ActionType: "wait_for_content",
			Message:    fmt.Sprintf("content did not stabilize within %v (phase: %s)", timeout, report.Phase),
		}
	}
	return nil
}

// ExecuteHover наводит мышь на элемент
func (e *ActionExecutor) ExecuteHover(ctx context.Context, selector string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for hover action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Msg("Hovering over element")

	// Используем JavaScript для hover (более надежно)
	err := chromedp.Evaluate(fmt.Sprintf(`
		(() => {
			const element = document.querySelector(%s);
			if (!element) throw new Error('Element not found');

			// Создаем и диспетчерим события мыши
			const mouseEnterEvent = new MouseEvent('mouseenter', {
				bubbles: true,
				cancelable: true,
				view: window
			});
			const mouseOverEvent = new MouseEvent('mouseover', {
				bubbles: true,
				cancelable: true,
				view: window
			});

			element.dispatchEvent(mouseEnterEvent);
			element.dispatchEvent(mouseOverEvent);
		})()
	`, quoteSelector(selector)), nil).Do(ctx)

	if err != nil {
		return fmt.Errorf("failed to hover over element %s: %w", selector, err)
	}

	// Ждем немного после hover (чтобы UI обновился)
	time.Sleep(200 * time.Millisecond)

	return nil
}

// ExecuteSelectOption выбирает опцию в dropdown
func (e *ActionExecutor) ExecuteSelectOption(ctx context.Context, selector, value string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for select_option action")
	}
	if value == "" {
		return fmt.Errorf("value is required for select_option action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Str("value", value).
		Msg("Selecting option")

	// Выбираем опцию через JavaScript (более надежно)
	err := chromedp.Evaluate(fmt.Sprintf(`
		(() => {
			const select = document.querySelector(%s);
			if (!select) throw new Error('Select element not found');

			// Ищем option с нужным value или текстом
			const options = Array.from(select.options);
			const option = options.find(opt =>
				opt.value === %s || opt.text.trim() === %s
			);

			if (!option) throw new Error('Option not found');

			select.value = option.value;
			select.dispatchEvent(new Event('change', { bubbles: true }));
		})()
	`, quoteSelector(selector), quoteString(value), quoteString(value)), nil).Do(ctx)

	if err != nil {
		return fmt.Errorf("failed to select option: %w", err)
	}

	return nil
}

// ExecuteJS выполняет JavaScript код и сохраняет результат
func (e *ActionExecutor) ExecuteJS(ctx context.Context, code string, actionIndex int) error {
	if code == "" {
		return fmt.Errorf("code is required for execute_js action")
	}

	e.logger.Debug().
		Str("code_length", fmt.Sprintf("%d", len(code))).
		Msg("Executing JavaScript")

	var result interface{}
	err := chromedp.Evaluate(code, &result).Do(ctx)

	// Сохраняем результат (upsert по actionIndex, чтобы при retry
	// не дублировать записи — перезаписываем предыдущую попытку)
	e.recordJSResult(JSResult{
		ActionIndex: actionIndex,
		Result:      result,
		Err:         err,
	})

	if err != nil {
		return fmt.Errorf("failed to execute JavaScript: %w", err)
	}

	e.logger.Debug().
		Interface("result", result).
		Msg("JavaScript executed")

	return nil
}

// ExecuteUploadFile загружает файл
func (e *ActionExecutor) ExecuteUploadFile(ctx context.Context, selector, filePath string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for upload_file action")
	}
	if filePath == "" {
		return fmt.Errorf("file_path is required for upload_file action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Str("file_path", filePath).
		Msg("Uploading file")

	// Загружаем файл
	err := chromedp.SendKeys(selector, filePath, chromedp.ByQuery).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	return nil
}

// ExecuteNavigate осуществляет навигацию по URL внутри текущей страницы.
// В отличие от execute_js с location.href, chromedp.Navigate() правильно
// дожидается page load event через CDP — новый документ полностью загружается
// перед переходом к следующему action.
//
// urlStr может быть относительным ("/path") или абсолютным ("https://example.com/path").
func (e *ActionExecutor) ExecuteNavigate(ctx context.Context, urlStr string) error {
	if urlStr == "" {
		return fmt.Errorf("url is required for navigate action")
	}

	e.logger.Debug().
		Str("url", urlStr).
		Msg("Navigating to URL")

	return chromedp.Navigate(urlStr).Do(ctx)
}

// quoteSelector returns s wrapped as a JSON string literal (including the
// surrounding double quotes) so it can be safely embedded as a JavaScript
// string argument in document.querySelector(...). The previous
// implementation used fmt.Sprintf(`"%s"`, s) — a raw double-quote wrap with
// no escaping — which produced invalid JavaScript whenever the selector
// itself contained double quotes. The very common selector
// input[name="login"] became:
//
//	document.querySelector("input[name="login"]")
//
// → "SyntaxError: missing ) after argument list". This silently broke the
// "clear field" step in ExecuteType on every login form, and likewise broke
// ExecuteHover / ExecuteSelectOption / ExecuteWaitForText for any selector
// or text containing a quote. json.Marshal handles double-quotes, backslashes
// and all other JS metacharacters correctly.
func quoteSelector(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a plain string only fails on \u0000 replacement;
		// fall back to a best-effort literal so the scrape never aborts.
		return fmt.Sprintf("%q", s)
	}
	return string(b)
}

// quoteString returns s as a JavaScript single-quoted string literal, with
// embedded single quotes, backslashes and newlines escaped. The previous
// implementation used fmt.Sprintf(`'%s'`, s) — a raw single-quote wrap — so a
// text value containing an apostrophe (e.g. "d'Artagnan") terminated the
// literal early and threw a SyntaxError in the page.
func quoteString(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// ParseActions из JSON (парсинг действий из MCP request)
func ParseActions(actionsData []interface{}) ([]Action, error) {
	var actions []Action

	for i, actionData := range actionsData {
		actionMap, ok := actionData.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("action %d is not a valid object", i)
		}

		action := Action{
			Retries: 0, // Использовать дефолт
			Timeout: 0, // Использовать дефолт
		}

		// Парсим обязательные поля
		actionType, ok := actionMap["type"].(string)
		if !ok || actionType == "" {
			return nil, fmt.Errorf("action %d missing required field: type", i)
		}
		action.Type = actionType

		// Парсим опциональные поля
		if selector, ok := actionMap["selector"].(string); ok {
			action.Selector = selector
		}

		if text, ok := actionMap["text"].(string); ok {
			action.Text = text
		}

		if value, ok := actionMap["value"].(string); ok {
			action.Value = value
		}

		if timeoutMs, ok := actionMap["timeout"].(float64); ok {
			action.Timeout = time.Duration(timeoutMs) * time.Millisecond
		}

		if retries, ok := actionMap["retries"].(float64); ok {
			action.Retries = int(retries)
		}

		// Login composite action fields (#77 Tier-2). These are top-level
		// string fields on Action; ParseActions must populate them from the
		// JSON the MCP client sends, or validateAction rejects the action as
		// "username_selector is required" before Chrome ever runs. This was
		// missed when login was added — the round-trip test exercised
		// json.Marshal/Unmarshal (PascalCase field tags), not ParseActions
		// (snake_case from a map[string]interface{}).
		if s, ok := actionMap["username_selector"].(string); ok {
			action.UsernameSelector = s
		}
		if s, ok := actionMap["username"].(string); ok {
			action.Username = s
		}
		if s, ok := actionMap["password_selector"].(string); ok {
			action.PasswordSelector = s
		}
		if s, ok := actionMap["password"].(string); ok {
			action.Password = s
		}
		if s, ok := actionMap["submit_selector"].(string); ok {
			action.SubmitSelector = s
		}

		// Extract structured action fields (#77 Tier-3). schema is an object
		// mapping output field names to {selector,attr}; container is an
		// optional CSS selector enabling array mode. Both arrive as nested
		// JSON via the MCP action; marshal the schema back to JSON and
		// unmarshal into the typed FieldSpec map so field names are validated
		// and attr defaults are applied centrally.
		if rawSchema, ok := actionMap["schema"]; ok {
			schema, serr := parseExtractSchema(rawSchema)
			if serr != nil {
				return nil, fmt.Errorf("action %d: %w", i, serr)
			}
			action.ExtractSchema = schema
		}
		if s, ok := actionMap["container"].(string); ok {
			action.Container = s
		}

		// Validate required fields eagerly. Rejecting an invalid action here
		// (before any Chrome work) avoids the failure mode where a single bad
		// action — e.g. wait_for with an empty selector — survives into
		// ExecuteActions, wastes 3+ seconds on doomed retries, then aborts the
		// entire action chain (including actions that already succeeded) and
		// falls back to HTTP. See issue #50.
		if err := validateAction(action, i); err != nil {
			return nil, err
		}

		actions = append(actions, action)
	}

	return actions, nil
}
