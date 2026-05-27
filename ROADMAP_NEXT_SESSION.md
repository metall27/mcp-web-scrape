# Roadmap: Next Session (Interactivity + Refactoring)

## Текущий статус: 7/9 задач выполнено ✅

### ✅ Выполнено:
1. ✅ Кэширование для scrape_with_js
2. ✅ Пул браузеров
3. ✅ Ротация User-Agent
4. ✅ Network Idle
5. ✅ Markdown конвертация
6. ✅ Stealth улучшения
7. ✅ Поддержка прокси

### 🔄 Осталось:
8. **Интерактивность** — click, type, scroll actions
9. **Рефакторинг** — единый интерфейс Scraper

---

## Часть 1: Интерактивность (Priority 1)

### Задача:
Добавить интерактивные действия в `scrape_with_js` для работы с login-protected контентом и динамическими элементами.

### Текущие ограничения:
- Только чтение контента
- Не может нажать кнопку, заполнить форму
- Не может прокрутить к конкретному элементу
- Нет возможности interaction с SPA после загрузки

### Требуемые Actions:

#### 1.1. Базовые действия (Priority: High)
```
- click(selector)          — кликнуть по элементу
- type(selector, text)      — ввести текст в поле
- submit(selector)          — отправить форму
- scroll_to(selector)      — прокрутить к элементу
- wait_for(selector, timeout) — ждать появления элемента
```

#### 1.2. Продвинутые действия (Priority: Medium)
```
- hover(selector)           — навести мышь (для dropdowns)
- select_option(selector, value) — выбрать в dropdown
- upload_file(selector, path)  — загрузить файл
- execute_js(code)         — выполнить JS код
- wait_for_text(text, timeout) — ждать текста на странице
```

#### 1.3. Навигация (Priority: Low)
```
- go_back()                 — назад
- go_forward()              — вперед
- refresh()                 — обновить страницу
```

### План реализации:

#### Шаг 1: Изменить API scrape_with_js
Добавить новое поле `actions` в параметры:
```json
{
  "url": "https://example.com/login",
  "actions": [
    {"type": "type", "selector": "#username", "text": "user"},
    {"type": "type", "selector": "#password", "text": "pass"},
    {"type": "click", "selector": "button[type='submit']"},
    {"type": "wait_for_text", "text": "Welcome"}
  ]
}
```

#### Шаг 2: Создать пакет `internal/pkg/browser/actions`
```go
// actions.go
type Action struct {
    Type     string
    Selector string
    Text     string
    Value    string
    Timeout  int
}

func ExecuteClick(ctx, selector string) error
func ExecuteType(ctx, selector, text string) error
func ExecuteWaitFor(ctx, selector string, timeout time.Duration) error
func ExecuteScrollTo(ctx, selector string) error
```

#### Шаг 3: Интеграция в js_tool.go
- Распарсить `actions` из параметров
- Выполнить действия после загрузки страницы
- Обработать ошибки (fallback, retry)
- Логирование каждого действия

#### Шаг 4: Тестирование
- Login форма на тестовом сайте
- SPA с lazy loading
- E-commerce с фильтрами
- Проверка обработки ошибок

### Структура файлов:
```
internal/pkg/browser/actions.go       # Основные действия
internal/mcp/tools/js_tool.go        # Интеграция
internal/mcp/tools/actions_test.go  # Тесты
examples/interactive/                # Примеры использования
```

### Ожидаемые сложности:
1. **Обработка ошибок** — что если элемент не найден?
2. **Retry логика** — сколько раз пытаться?
3. **Timeouts** — разумные дефолты
4. **Screenshot после действий** — для визуального контроля
5. **Совместимость с кэшем** — действия не кэшируются

---

## Часть 2: Рефакторинг (Priority 2)

### Текущие проблемы:
1. **Дубликация кода** — `ScrapeTool` и `ScrapeJSTool` имеют общую логику
2. **Разные интерфейсы** — нет единого `Scraper` интерфейса
3. **Сложность поддержки** — изменения нужно делать в 2 местах

### План рефакторинга:

#### Шаг 1: Создать единый интерфейс
```go
// internal/mcp/tools/scraper.go
type Scraper interface {
    Scrape(ctx context.Context, url string, opts ScrapeOptions) (*ScrapeResult, error)
    Name() string
    SupportsJS() bool
}

type ScrapeOptions struct {
    Timeout       time.Duration
    UserAgent     string
    WaitFor       string
    WaitTime      time.Duration
    Screenshot    bool
    OutputFormat  string
    Stealth       *browser.StealthConfig
    Actions       []Action
}

type ScrapeResult struct {
    HTML       string
    URL        string
    StatusCode int
    Title       string
    Screenshot  []byte
    Duration    time.Duration
    Metadata    map[string]interface{}
}
```

#### Шаг 2: Рефакторинг существующих инструментов
```go
// HTTPScraper (было ScrapeTool)
type HTTPScraper struct {
    cache     *cache.Cache
    client    *http.Client
    converter *converter.Converter
    uaRotator *useragent.Rotator
    proxy     *proxy.Rotator
}

func (s *HTTPScraper) Scrape(ctx, url string, opts ScrapeOptions) (*ScrapeResult, error)

// ChromeScraper (было ScrapeJSTool)
type ChromeScraper struct {
    cache       *cache.Cache
    browserPool *browser.Pool
    converter   *converter.Converter
    uaRotator   *useragent.Rotator
    proxy       *proxy.Rotator
}

func (s *ChromeScraper) Scrape(ctx, url string, opts ScrapeOptions) (*ScrapeResult, error)
```

#### Шаг 3: Единая точка входа
```go
// internal/mcp/tools/unified_scraper.go
type UnifiedScraper struct {
    http   *HTTPScraper
    chrome *ChromeScraper
}

func (s *UnifiedScraper) Scrape(ctx, url string, opts ScrapeOptions) (*ScrapeResult, error) {
    // Auto-select based on options
    if opts.Actions != nil && len(opts.Actions) > 0 {
        return s.chrome.Scrape(ctx, url, opts)
    }

    // Try HTTP first, fallback to Chrome
    result, err := s.http.Scrape(ctx, url, opts)
    if err != nil {
        return s.chrome.Scrape(ctx, url, opts)
    }
    return result, nil
}
```

#### Шаг 4: Миграция MCP инструментов
- Изменить `registerDefaultTools()` в server.go
- Обновить схемы инструментов
- Сохранить обратную совместимость

### Преимущества рефакторинга:
1. **Меньше кода** — убрать дубликацию
2. **Проще поддержка** — изменения в одном месте
3. **Единый API** — `Scrape()` вместо разных функций
4. **Тестируемость** — легче писать тесты
5. **Расширяемость** — добавить новый scraper (например, Playwright) проще

### Структура файлов после рефакторинга:
```
internal/mcp/tools/
  scraper.go           # Интерфейс Scraper
  http_scraper.go      # Реализация HTTP
  chrome_scraper.go    # Реализация Chrome
  unified_scraper.go  # Единая точка входа
  scraper_test.go      # Тесты
```

---

## Порядок выполнения в следующей сессии:

### Phase 1: Интерактивность (2-3 часа)
1. ✅ Создать `internal/pkg/browser/actions.go`
2. ✅ Обновить схему `scrape_with_js` с полем `actions`
3. ✅ Интегрировать execution в `js_tool.go`
4. ✅ Добавить обработку ошибок и retry логику
5. ✅ Тестирование на реальных сайтах
6. ✅ Обновить документацию

### Phase 2: Рефакторинг (2-3 часа)
1. ✅ Создать интерфейс `Scraper`
2. ✅ Рефакторить `ScrapeTool` → `HTTPScraper`
3. ✅ Рефакторить `ScrapeJSTool` → `ChromeScraper`
4. ✅ Создать `UnifiedScraper`
5. ✅ Миграция MCP server
6. ✅ Тестирование backward compatibility
7. ✅ Обновить README

### Итого: 4-6 часов работы в следующей сессии

---

## Примеры использования интерактивности:

### Пример 1: Login на сайт
```json
{
  "url": "https://example.com/login",
  "actions": [
    {"type": "type", "selector": "#username", "text": "myuser"},
    {"type": "type", "selector": "#password", "text": "mypass"},
    {"type": "click", "selector": "button[type='submit']"},
    {"type": "wait_for_text", "text": "Welcome", "timeout": 10000}
  ]
}
```

### Пример 2: Работа с фильтрами
```json
{
  "url": "https://shop.example.com/products",
  "actions": [
    {"type": "scroll_to", "selector": "#filters"},
    {"type": "click", "selector": "button[data-filter='price']"},
    {"type": "wait_for", "selector": ".products-grid", "timeout": 5000}
  ]
}
```

### Пример 3: Lazy loading
```json
{
  "url": "https://news.example.com",
  "actions": [
    {"type": "scroll_to", "selector": "footer"},
    {"type": "wait_for", "selector": ".article:nth-child(10)", "timeout": 5000}
  ]
}
```

---

## Файлы для создания в следующей сессии:

### Новые файлы:
- `internal/pkg/browser/actions.go`
- `internal/mcp/tools/actions_test.go`
- `internal/mcp/tools/scraper.go` (интерфейс)
- `internal/mcp/tools/http_scraper.go`
- `internal/mcp/tools/chrome_scraper.go`
- `internal/mcp/tools/unified_scraper.go`
- `internal/mcp/tools/scraper_test.go`
- `examples/interactive/README.md`
- `examples/interactive/login_example.json`
- `examples/interactive/filters_example.json`

### Изменить файлы:
- `internal/mcp/tools/js_tool.go` (интеграция actions)
- `internal/mcp/tools/scraper.go` (HTTP scraper)
- `internal/mcp/server.go` (регистрация)

---

## Критерии успеха:

### Интерактивность:
- ✅ Может залогиниться на тестовом сайте
- ✅ Может заполнить и отправить форму
- ✅ Может работать с lazy loading контентом
- ✅ Graceful error handling при неудачных actions
- ✅ Логирует каждое действие
- ✅ Совместимо с кэшем (actions не кэшируются)

### Рефакторинг:
- ✅ Убрана дубликация кода
- ✅ Единый интерфейс `Scraper`
- ✅ Обратная совместимость API
- ✅ Все тесты проходят
- ✅ Документация обновлена
- ✅ Примеры использования добавлены

---

## Note для Claude (следующая сессия):

1. **Начни с интерактивности** — это более приоритетно
2. **Создай `actions.go` первым делом** — базовая инфраструктура
3. **Тестируй по мере реализации** — не делай всё сразу
4. **Рефакторинг делай осторожно** — сохраняй backward compatibility
5. **Используй этот план** — он уже детально продуман

---

## Дополнительные идеи (future):

### Advanced Interactivity:
- Drag & drop
- Multi-select
- File upload с прогрессом
- Iframes handling
- WebSockets interaction

### Scrapers:
- Playwright scraper (больше возможностей)
- Headless Firefox (для diversity)
- Mobile emulation (responsive design)

### Monitoring:
- Per-proxy statistics
- Per-site statistics
- Success rates tracking
- Performance metrics

---

**Total completion: 7/9 (78%)**

**Next session goals: Interactivity + Refactoring = 100%**
