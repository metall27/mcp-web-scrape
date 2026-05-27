package browser

import (
	"context"
	"fmt"
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

// ActionExecutor исполнитель действий
type ActionExecutor struct {
	logger    zerolog.Logger
	stealth   *StealthActions
	retries   int           // Дефолтное количество ретраев
	timeout   time.Duration // Дефолтный timeout
}

// NewActionExecutor создает новый экземпляр ActionExecutor
func NewActionExecutor(logger zerolog.Logger, stealth *StealthActions) *ActionExecutor {
	return &ActionExecutor{
		logger:  logger,
		stealth: stealth,
		retries: 3,               // Дефолт: 3 ретрая
		timeout: 30 * time.Second, // Дефолт: 30s timeout
	}
}

// ExecuteActions выполняет список действий последовательно
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

		// Выполняем действие с ретраями
		var lastErr error
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

			// Выполняем действие
			err := e.ExecuteAction(actionCtx, action)
			cancel()

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
			e.logger.Warn().
				Int("action_number", i+1).
				Str("type", action.Type).
				Int("attempt", attempt+1).
				Err(err).
				Msg("Action failed")
		}

		// Если все ретраи провалились
		if lastErr != nil {
			return fmt.Errorf("action %d (%s on %s) failed after %d attempts: %w",
				i+1, action.Type, action.Selector, retries, lastErr)
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

// ExecuteAction выполняет одно действие
func (e *ActionExecutor) ExecuteAction(ctx context.Context, action Action) error {
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
		return e.ExecuteJS(ctx, action.Text)
	case "upload_file":
		return e.ExecuteUploadFile(ctx, action.Selector, action.Text)
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
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

	// Сначала кликаем на поле (чтобы оно获得 фокус)
	err := chromedp.Focus(selector, chromedp.ByQuery).Do(ctx)
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

// ExecuteSubmit отправляет форму
func (e *ActionExecutor) ExecuteSubmit(ctx context.Context, selector string) error {
	if selector == "" {
		return fmt.Errorf("selector is required for submit action")
	}

	e.logger.Debug().
		Str("selector", selector).
		Msg("Submitting form")

	// Кликаем на кнопку/элемент submit
	err := chromedp.Submit(selector, chromedp.ByQuery).Do(ctx)
	if err != nil {
		// Fallback: обычный клик
		return e.ExecuteClick(ctx, selector)
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

// ExecuteWaitFor ждет появления элемента
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
		return fmt.Errorf("element %s not visible within %v: %w", selector, timeout, err)
	}

	return nil
}

// ExecuteWaitForText ждет появления текста на странице
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
			return fmt.Errorf("text '%s' not found within %v", text, timeout)
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

// ExecuteJS выполняет JavaScript код
func (e *ActionExecutor) ExecuteJS(ctx context.Context, code string) error {
	if code == "" {
		return fmt.Errorf("code is required for execute_js action")
	}

	e.logger.Debug().
		Str("code_length", fmt.Sprintf("%d", len(code))).
		Msg("Executing JavaScript")

	var result interface{}
	err := chromedp.Evaluate(code, &result).Do(ctx)
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

// Helper функции для экранирования строк

func quoteSelector(s string) string {
	// Для CSS selector используем двойные кавычки внутри шаблона
	return fmt.Sprintf(`"%s"`, s)
}

func quoteString(s string) string {
	// Экранируем строку для JavaScript
	return fmt.Sprintf(`'%s'`, s)
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
			Retries: 0,      // Использовать дефолт
			Timeout: 0,      // Использовать дефолт
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

		actions = append(actions, action)
	}

	return actions, nil
}