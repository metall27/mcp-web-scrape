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
	Type     string        // Тип действия: click, type, scroll_to, wait_for, etc.
	Selector string        // CSS selector для элемента
	Text     string        // Текст для ввода (для type)
	Value    string        // Значение (для select_option)
	Timeout  time.Duration // Timeout для ожидания
	Retries  int           // Количество ретраев при ошибке
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
type PageObservation struct {
	ActionIndex int      `json:"action_index"` // 0-based index of the preceding action
	URL         string   `json:"url"`          // window.location.href after the action
	URLChanged  bool     `json:"url_changed"`  // true if URL differs from previous observation
	Title       string   `json:"title"`
	Headings    []string `json:"headings"`     // up to 5 h1/h2 texts (truncated to 100 chars)
	Errors      []string `json:"errors"`       // visible error/alert messages (up to 5)
	BodyChanged bool     `json:"body_changed"` // true if body content hash changed
	TextPreview string   `json:"text_preview"` // first ~300 chars of visible body text
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
	"click":         {{"selector", func(a Action) string { return a.Selector }}},
	"type":          {{"selector", func(a Action) string { return a.Selector }}, {"text", func(a Action) string { return a.Text }}},
	"submit":        {{"selector", func(a Action) string { return a.Selector }}},
	"scroll_to":     {{"selector", func(a Action) string { return a.Selector }}},
	"wait_for":      {{"selector", func(a Action) string { return a.Selector }}},
	"wait_for_text": {{"text", func(a Action) string { return a.Text }}},
	"hover":         {{"selector", func(a Action) string { return a.Selector }}},
	"select_option": {{"selector", func(a Action) string { return a.Selector }}, {"value", func(a Action) string { return a.Value }}},
	"execute_js":    {{"text", func(a Action) string { return a.Text }}},
	"upload_file":   {{"selector", func(a Action) string { return a.Selector }}, {"text", func(a Action) string { return a.Text }}},
	"navigate":      {{"text", func(a Action) string { return a.Text }}},
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

// ActionExecutor исполнитель действий
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
func NewActionExecutor(logger zerolog.Logger, stealth *StealthActions, observeChanges bool) *ActionExecutor {
	return &ActionExecutor{
		logger:  logger,
		stealth: stealth,
		retries: 3,                // Дефолт: 3 ретрая
		timeout: 30 * time.Second, // Дефолт: 30s timeout
		observe: observeChanges,
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

		// Observation после mutating action (#72).
		if e.observe && isMutatingAction(action.Type) {
			if err := e.captureObservation(ctx, i); err != nil {
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
	case "click", "submit", "type", "select_option", "upload_file", "navigate", "execute_js":
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
// error/alert texts, body hash and a text preview — all bounded to keep the
// response small (~50-150 tokens). Designed so the LLM can judge whether a
// login/navigation/loading action had the intended effect without guessing the
// exact text to wait_for. See #72.
func (e *ActionExecutor) captureObservation(ctx context.Context, actionIndex int) error {
	var probe struct {
		URL      string   `json:"url"`
		Title    string   `json:"title"`
		Headings []string `json:"headings"`
		Errors   []string `json:"errors"`
		Hash     string   `json:"hash"`
		Preview  string   `json:"preview"`
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
		return {
			url: location.href,
			title: document.title || '',
			headings: heads,
			errors: errs,
			hash: String(h),
			preview: trunc(trimmed, 300)
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
