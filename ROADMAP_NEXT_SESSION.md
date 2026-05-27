# Roadmap: Next Session — Final Phase (Refactoring)

## Текущий статус: 8/9 задач выполнено ✅ (89%)

### ✅ Выполнено (прошлые сессии):
1. ✅ Кэширование для scrape_with_js
2. ✅ Пул браузеров
3. ✅ Ротация User-Agent
4. ✅ Network Idle
5. ✅ Markdown конвертация
6. ✅ Stealth улучшения
7. ✅ Поддержка прокси
8. ✅ **Интерактивность** — click, type, scroll actions (ВЫПОЛНЕНО в этой сессии!)

### 🔄 Осталось (ЭТА СЕССИЯ):
9. **Рефакторинг** — единый интерфейс Scraper (ПОСЛЕДНЯЯ ЗАДАЧА!)

---

## Цель следующей сессии: REFACTORING (100% завершенность)

### Задача:
Создать единый интерфейс `Scraper` для устранения дубликации кода и упрощения поддержки.

### Проблема (текущая):
```go
// ДВА ОТДЕЛЬНЫХ ИНСТРУМЕНТА БЕЗ ОБЩЕГО ИНТЕРФЕЙСА
scrape_url := NewScrapeTool(cache)           // HTTP (301 строка)
scrape_with_js := NewScrapeJSTool(...)       // Chrome (849 строк)

// ДУБЛИКАЦИЯ КОДА:
- getCacheKey() в обоих файлах
- Логика кэширования duplicated
- HTML оптимизация duplicated
- Валидация URL duplicated
- Result struct разный в каждом
```

### Решение (рефакторинг):
```go
// ЕДИНЫЙ ИНТЕРФЕЙС ДЛЯ ВСЕХ СКРАПЕРОВ
type Scraper interface {
    Scrape(ctx, url string, opts Options) (*Result, error)
    Name() string
    SupportsJS() bool
    SupportsActions() bool
}

// УНИФИЦИРОВАННЫЙ СКРАПЕР
scrapers := []Scraper{
    NewHTTPScraper(cache, uaRotator, proxy),      // 200 строк (-33%)
    NewChromeScraper(cache, browserPool, uaRotator, proxy), // 600 строк (-29%)
}

unified := NewUnifiedScraper(scrapers)            // Авто-выбор
result, err := unified.Scrape(ctx, url, opts)     // Единый API
```

---

## План реализации (2-3 часа)

### Phase 1: Архитектура (30 минут) ⏱️

#### 1.1. Создать интерфейс Scraper
**Файл:** `internal/mcp/tools/scraper.go`

```go
package tools

import (
    "context"
    "time"
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

    // Stealth
    StealthEnabled bool
    StealthScroll bool
    StealthMouse bool

    // Proxy (не используется в HTTPScraper, только в ChromeScraper)
    ProxyEnabled bool

    // Interactive actions (только ChromeScraper)
    Actions []Action
}

// Result общий результат для всех скраперов
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
}

// ActionsMetadata метаданные о выполненных действиях
type ActionsMetadata struct {
    Count int
    Types []string
}
```

#### 1.2. Создать общие функции
**Файл:** `internal/mcp/tools/common.go`

```go
package tools

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "net/url"
)

// Common shared functions for all scrapers

// ValidateURL проверяет URL
func ValidateURL(urlStr string) (*url.URL, error) {
    parsedURL, err := url.Parse(urlStr)
    if err != nil {
        return nil, fmt.Errorf("invalid URL: %w", err)
    }

    if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
        return nil, fmt.Errorf("only http and https schemes are supported")
    }

    return parsedURL, nil
}

// GenerateCacheKey генерирует ключ кэша
func GenerateCacheKey(url string, params map[string]interface{}) string {
    hash := sha256.New()
    hash.Write([]byte(url))

    // Include parameters in hash
    for key, val := range params {
        hash.Write([]byte(fmt.Sprintf("%s:%v", key, val)))
    }

    return "scrape:" + hex.EncodeToString(hash.Sum(nil))[:16]
}
```

---

### Phase 2: HTTPScraper (45 минут) ⏱️

#### 2.1. Рефакторить scraper.go → http_scraper.go
**Файл:** `internal/mcp/tools/http_scraper.go`

```go
package tools

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "time"
)

// HTTPScraper скрапер для статических сайтов (HTTP)
type HTTPScraper struct {
    cache     *cache.Cache
    uaRotator *useragent.Rotator
    proxy     *proxy.Rotator
    client    *http.Client
    logger    zerolog.Logger
}

// NewHTTPScraper создает новый HTTPScraper
func NewHTTPScraper(cache *cache.Cache, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *HTTPScraper {
    return &HTTPScraper{
        cache:     cache,
        uaRotator: uaRotator,
        proxy:     proxy,
        logger:    logger.Get(),
    }
}

// Scrape реализует интерфейс Scraper
func (s *HTTPScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
    // 1. Validate URL
    if _, err := ValidateURL(url); err != nil {
        return nil, err
    }

    // 2. Check cache
    if s.cache != nil && s.cache.IsEnabled() {
        cacheKey := GenerateCacheKey(url, optsToMap(opts))
        if cached, found := s.cache.Get(ctx, cacheKey); found {
            return &Result{
                HTML:      string(cached.Data),
                URL:       url,
                FromCache: true,
                // ... другие поля
            }, nil
        }
    }

    // 3. Create HTTP client
    client := &http.Client{
        Timeout: opts.Timeout,
    }

    // 4. Add proxy if enabled
    if s.proxy.IsEnabled() {
        client.Transport = &http.Transport{
            Proxy: s.proxy.GetProxyFunc(),
        }
    }

    // 5. Make request
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

    // Set headers
    if opts.UserAgent != "" {
        req.Header.Set("User-Agent", opts.UserAgent)
    } else if s.uaRotator != nil {
        req.Header.Set("User-Agent", s.uaRotator.Get())
    }

    // 6. Execute request
    resp, err := client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("HTTP request failed: %w", err)
    }
    defer resp.Body.Close()

    // 7. Read body
    body, _ := io.ReadAll(resp.Body)

    // 8. Optimize HTML
    html := string(OptimizeHTML(body))

    // 9. Build result
    result := &Result{
        HTML:        html,
        URL:         url,
        FinalURL:    url,
        StatusCode:  resp.StatusCode,
        ContentType: resp.Header.Get("Content-Type"),
        Duration:    time.Since(startTime),
        SizeBytes:   len(html),
        Format:      opts.OutputFormat,
        FromCache:   false,
    }

    // 10. Store in cache
    if s.cache != nil && s.cache.IsEnabled() {
        cacheKey := GenerateCacheKey(url, optsToMap(opts))
        cachedResp := &cache.CachedResponse{
            Data:      []byte(html),
            Timestamp: time.Now(),
            Headers: map[string]string{
                "content_type": resp.Header.Get("Content-Type"),
            },
        }
        s.cache.Set(ctx, cacheKey, cachedResp, ttl)
    }

    return result, nil
}

// Name возвращает название скрапера
func (s *HTTPScraper) Name() string {
    return "HTTP"
}

// SupportsJS возвращает false
func (s *HTTPScraper) SupportsJS() bool {
    return false
}

// SupportsActions возвращает false
func (s *HTTPScraper) SupportsActions() bool {
    return false
}

// Helper: convert Options to map for cache key
func optsToMap(opts Options) map[string]interface{} {
    return map[string]interface{}{
        "user_agent": opts.UserAgent,
        "timeout":    opts.Timeout.String(),
        "format":     opts.OutputFormat,
    }
}
```

---

### Phase 3: ChromeScraper (60 минут) ⏱️

#### 3.1. Рефакторить js_tool.go → chrome_scraper.go
**Файл:** `internal/mcp/tools/chrome_scraper.go`

```go
package tools

import (
    "context"
    "fmt"
    "time"

    "github.com/chromedp/chromedp"
    "github.com/metall/mcp-web-scrape/internal/pkg/browser"
)

// ChromeScraper скрапер для динамических сайтов (JavaScript)
type ChromeScraper struct {
    cache       *cache.Cache
    browserPool *browser.Pool
    uaRotator   *useragent.Rotator
    proxy       *proxy.Rotator
    converter   *converter.Converter
    logger      zerolog.Logger
}

// NewChromeScraper создает новый ChromeScraper
func NewChromeScraper(cache *cache.Cache, browserPool *browser.Pool, uaRotator *useragent.Rotator, proxy *proxy.Rotator) *ChromeScraper {
    return &ChromeScraper{
        cache:       cache,
        browserPool: browserPool,
        uaRotator:   uaRotator,
        proxy:       proxy,
        converter:   converter.New(),
        logger:      logger.Get(),
    }
}

// Scrape реализует интерфейс Scraper
func (s *ChromeScraper) Scrape(ctx context.Context, url string, opts Options) (*Result, error) {
    // 1. Validate URL
    if _, err := ValidateURL(url); err != nil {
        return nil, err
    }

    // 2. Check cache (bypass if actions present)
    hasActions := len(opts.Actions) > 0
    if s.cache != nil && s.cache.IsEnabled() && !hasActions {
        cacheKey := GenerateCacheKey(url, optsToMap(opts))
        if cached, found := s.cache.Get(ctx, cacheKey); found {
            return &Result{
                HTML:      string(cached.Data),
                URL:       url,
                FromCache: true,
            }, nil
        }
    }

    // 3. Get browser context
    browserCtx, browserCancel, err := s.browserPool.GetContext(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to get browser context: %w", err)
    }
    defer browserCancel()

    // 4. Build Chrome tasks
    tasks := s.buildChromeTasks(url, opts)

    // 5. Run tasks
    if err := chromedp.Run(browserCtx, tasks...); err != nil {
        // HTTP fallback?
        return nil, fmt.Errorf("Chrome scraping failed: %w", err)
    }

    // 6. Optimize and convert HTML
    html := string(OptimizeHTML([]byte(htmlData)))
    if opts.OutputFormat == "markdown" {
        html, _ = s.converter.Convert(html, converter.FormatMarkdown)
    }

    // 7. Build result
    result := &Result{
        HTML:           html,
        URL:            url,
        FinalURL:       finalURL,
        Title:          title,
        StatusCode:     200,
        ContentType:    "text/html",
        Duration:       time.Since(startTime),
        SizeBytes:      len(html),
        Screenshot:     screenshotData,
        Format:         opts.OutputFormat,
        FromCache:      false,
        ActionsMetadata: actionsMetadata,
    }

    // 8. Store in cache (only if no actions)
    if s.cache != nil && s.cache.IsEnabled() && !hasActions {
        cacheKey := GenerateCacheKey(url, optsToMap(opts))
        // ... store in cache
    }

    return result, nil
}

// Name возвращает название скрапера
func (s *ChromeScraper) Name() string {
    return "Chrome"
}

// SupportsJS возвращает true
func (s *ChromeScraper) SupportsJS() bool {
    return true
}

// SupportsActions возвращает true
func (s *ChromeScraper) SupportsActions() bool {
    return true
}

// buildChromeTasks строит список Chrome задач
func (s *ChromeScraper) buildChromeTasks(url string, opts Options) []chromedp.Action {
    tasks := []chromedp.Action{
        chromedp.Navigate(url),
        chromedp.WaitReady("body", chromedp.ByQuery),
    }

    // Wait strategies
    if opts.WaitForNetworkIdle {
        tasks = append(tasks, browser.NetworkIdleAdvanced(...))
    } else {
        tasks = append(tasks, chromedp.Sleep(opts.WaitForDuration))
    }

    // Interactive actions
    if len(opts.Actions) > 0 {
        actionExecutor := browser.NewActionExecutor(s.logger, stealth)
        tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
            return actionExecutor.ExecuteActions(ctx, opts.Actions)
        }))
    }

    // Get content
    tasks = append(tasks,
        chromedp.OuterHTML("html", &htmlData, chromedp.ByQuery),
        chromedp.Title(&title),
        chromedp.Location(&finalURL),
    )

    // Screenshot
    if opts.Screenshot {
        tasks = append(tasks, chromedp.FullScreenshot(&screenshotData, 90))
    }

    return tasks
}
```

---

### Phase 4: UnifiedScraper (30 минут) ⏱️

#### 4.1. Создать unified_scraper.go
**Файл:** `internal/mcp/tools/unified_scraper.go`

```go
package tools

import (
    "context"
    "fmt"
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
    result.Method = selectedScraper.Name()

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
    return s.scrapers[0]
}

// needsJavaScript определяет нужен ли JavaScript
func (s *UnifiedScraper) needsJavaScript(url string, opts Options) bool {
    // Явные требования
    if opts.WaitForNetworkIdle {
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
    }

    for _, site := range jsSites {
        if contains(url, site) {
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
                return result, nil
            }
        }
    }

    return nil, fmt.Errorf("all scrapers failed: %w", originalErr)
}

func contains(s, substr string) bool {
    return len(s) >= len(substr) && (s == substr ||
        len(s) > len(substr) && (s[:len(substr)] == substr ||
        s[len(s)-len(substr):] == substr ||
        containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
    for i := 0; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
            return true
        }
    }
    return false
}
```

---

### Phase 5: Интеграция (15 минут) ⏱️

#### 5.1. Обновить internal/mcp/server.go
**Файл:** `internal/mcp/server.go`

```go
// Создать скраперы
httpScraper := NewHTTPScraper(cache, uaRotator, proxy)
chromeScraper := NewChromeScraper(cache, browserPool, uaRotator, proxy)

// Создать unified scraper
unifiedScraper := NewUnifiedScraper([]Scraper{
    httpScraper,
    chromeScraper,
})

// Регистрация инструментов
registerDefaultTools(mcps, cache, browserPool, ragConfig, uaRotator, proxy, unifiedScraper)
```

#### 5.2. Обновить registerDefaultTools
```go
func registerDefaultTools(mcps *mcp.MCPServer, cache *cache.Cache, browserPool *browser.Pool, ragConfig config.RAGConfig, uaRotator *useragent.Rotator, proxyRotator *proxy.Rotator, unifiedScraper *UnifiedScraper) {
    // Регистрируем инструменты
    scrapeURL := NewScrapeURLTool(unifiedScraper) // Использует unified scraper
    scrapeWithJS := NewScrapeWithJSTool(unifiedScraper) // Использует unified scraper
    // ...
}
```

---

### Phase 6: Тестирование (30 минут) ⏱️

#### 6.1. Unit тесты
**Файл:** `internal/mcp/tools/scraper_test.go`

```go
package tools

import (
    "context"
    "testing"
    "time"
)

func TestHTTPScraper(t *testing.T) {
    scraper := NewHTTPScraper(nil, nil, nil)

    if scraper.Name() != "HTTP" {
        t.Errorf("Expected name 'HTTP', got '%s'", scraper.Name())
    }

    if scraper.SupportsJS() {
        t.Error("HTTPScraper should not support JS")
    }
}

func TestChromeScraper(t *testing.T) {
    scraper := NewChromeScraper(nil, nil, nil, nil)

    if scraper.Name() != "Chrome" {
        t.Errorf("Expected name 'Chrome', got '%s'", scraper.Name())
    }

    if !scraper.SupportsJS() {
        t.Error("ChromeScraper should support JS")
    }
}

func TestUnifiedScraper(t *testing.T) {
    httpScraper := NewHTTPScraper(nil, nil, nil)
    chromeScraper := NewChromeScraper(nil, nil, nil, nil)

    unified := NewUnifiedScraper([]Scraper{
        httpScraper,
        chromeScraper,
    })

    if !unified.SupportsJS() {
        t.Error("UnifiedScraper should support JS (has ChromeScraper)")
    }
}
```

#### 6.2. Integration тесты
```go
func TestUnifiedScraperIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    // Создать реальные скраперы
    cache := cache.New(cache.Config{Enabled: false})
    browserPool := setupTestBrowserPool()
    uaRotator := useragent.NewRotator(nil)
    proxy := proxy.NewRotator(nil)

    httpScraper := NewHTTPScraper(cache, uaRotator, proxy)
    chromeScraper := NewChromeScraper(cache, browserPool, uaRotator, proxy)

    unified := NewUnifiedScraper([]Scraper{httpScraper, chromeScraper})

    // Тест 1: Простой сайт (должен выбрать HTTP)
    result1, err := unified.Scrape(context.Background(), "https://example.com", Options{
        Timeout: 30 * time.Second,
    })

    if err != nil {
        t.Fatalf("Failed to scrape example.com: %v", err)
    }

    if result1.Method != "HTTP" {
        t.Logf("Note: example.com used %s scraper", result1.Method)
    }

    // Тест 2: JavaScript сайт (должен выбрать Chrome)
    result2, err := unified.Scrape(context.Background(), "https://github.com", Options{
        Timeout: 60 * time.Second,
    })

    if err != nil {
        t.Fatalf("Failed to scrape github.com: %v", err)
    }

    if result2.Method != "Chrome" {
        t.Errorf("Expected Chrome for GitHub, got %s", result2.Method)
    }
}
```

---

## Ожидаемые результаты после рефакторинга:

### ✅ Улучшения:

1. **Меньше кода:** 1150 → 900 строк (-22%)
2. **Нет дубликации:** Общая логика в одном месте
3. **Единый API:** `Scrape(ctx, url, opts)` для всех
4. **Авто-выбор:** UnifiedScraper выбирает лучший метод
5. **Проще тестировать:** Mock интерфейса Scraper
6. **Легче расширять:** Добавить новый скрапер = реализовать интерфейс

### 📁 Файловая структура:

```
internal/mcp/tools/
├── scraper.go           # ✅ Интерфейс Scraper + Options/Result
├── common.go            # ✅ Общие функции (ValidateURL, GenerateCacheKey)
├── http_scraper.go      # ✅ HTTPScraper (scraper.go → переименован)
├── chrome_scraper.go    # ✅ ChromeScraper (js_tool.go → переименован)
├── unified_scraper.go   # ✅ UnifiedScraper (новый файл)
├── tool.go              # Без изменений
├── html_optimizer.go    # Без изменений
├── parser.go            # Без изменений
├── search.go            # Без изменений
└── scraper_test.go      # ✅ Обновить тесты
```

### 🎯 Итог: 9/9 задач (100%) 🎉

---

## Дополнительные улучшения (future):

### После рефакторинга легко добавить:
- ✅ PlaywrightScraper
- ✅ FirefoxScraper
- ✅ MobileScraper
- ✅ ScreenshotScraper
- ✅ PDFScraper
- ✅ APIScraper

---

**Старт следующей сессии: готово!** 🚀

Все файлы и структура подготовлены для быстрого старта рефакторинга.

Время: 2-3 часа
Результат: 100% завершенность проекта