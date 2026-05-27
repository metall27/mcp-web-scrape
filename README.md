# MCP Web Scrape Server

**Status:** ✅ 100% Complete | Production Ready

MCP-сервер для веб-скрапинга с унифицированной архитектурой. Работает с llama.cpp WebUI, Claude Desktop и другими MCP-совместимыми AI агентами.

## 🎯 Возможности

### Основные функции:
- **Двойной движок** — HTTP scraper для статических сайтов, Chrome scraper для динамических
- **JavaScript-рендеринг** — headless Chrome для SPA, дашбордов, GitHub
- **Интерактивные действия** — click, type, scroll для работы с login-protected контентом
- **Markdown экспорт** — конвертация HTML в Markdown для LLM
- **Авто-оптимизация** — уменьшает HTML на 70-95% для экономии токенов
- **Скриншоты** — автоматические для больших страниц (>50КБ)
- **Smart caching** — кэширование с TTL по типу контента
- **HTTP fallback** — автоматическое переключение при проблемах с Chrome

### Продвинутые функции:
- **Network Idle** — умное ожидание загрузки SPA (30 сек timeout)
- **Proxy rotation** — ротация прокси для обхода блокировок
- **User-Agent rotation** — случайные UA для stealth mode
- **Browser pool** — переиспользование браузеров для производительности
- **Stealth mode** — эмуляция человеческого поведения (delays, scroll, mouse)
- **RAG интеграция** — авто-индексирование для семантического поиска

### Архитектура:
- **Унифицированный интерфейс** — все scrapers реализуют `Scraper` interface
- **Авто-выбор метода** — `UnifiedScraper` выбирает HTTP vs Chrome автоматически
- **Fallback логика** — при ошибке пробует следующий scraper
- **Модульная структура** — легко добавлять новые scrapers

## 🛠️ MCP-инструменты

### `scrape_url` (HTTP scraper)

Быстрый HTTP scraper для **статических сайтов**: блоги, новости, документация.

```json
{
  "url": "https://example.com/blog/post",
  "timeout": 30,
  "user_agent": "CustomBot/1.0"
}
```

**Параметры:**
- `url` (обязательный) — URL для скрапинга
- `timeout` — таймаут в секундах (по умолчанию 30)
- `user_agent` — кастомный User-Agent
- `headers` — кастомные HTTP заголовки

**Особенности:**
- ⚡ Быстрый (1-2 сек)
- 💾 Низкое потребление памяти
- 🔄 Поддержка прокси и UA rotation
- 🎯 Оптимален для статического контента

### `scrape_with_js` (Chrome scraper)

Универсальный инструмент для **динамических сайтов**: GitHub, SPA, дашборды.

```json
{
  "url": "https://github.com/user/repo",
  "wait_for_network_idle": true,
  "output_format": "markdown",
  "screenshot_mode": "auto"
}
```

**Параметры:**
- `url` (обязательный) — URL для скрапинга
- `timeout` — таймаут в секундах (по умолчанию 60)
- `wait_for` — CSS селектор для ожидания
- `wait_time` — задержка после загрузки в мс (по умолчанию 3000)
- `wait_for_network_idle` — умное ожидание загрузки (30 сек timeout)
- `screenshot_mode` — когда делать скриншот: `"auto"`, `"always"`, `"never"`
- `output_format` — формат вывода: `"html"` (по умолчанию) или `"markdown"`
- `block_images` — блокировать картинки для ускорения
- `user_agent` — кастомный User-Agent
- `stealth_enabled` — включить stealth mode
- `stealth_scroll` — эмуляция скролла (по умолчанию true)
- `stealth_mouse` — эмуляция движений мыши

**Интерактивные действия** (click, type, scroll):
```json
{
  "url": "https://example.com/login",
  "actions": [
    {"type": "type", "selector": "#username", "text": "user"},
    {"type": "type", "selector": "#password", "text": "pass"},
    {"type": "click", "selector": "button[type='submit']"},
    {"type": "wait_for_text", "text": "Welcome", "timeout": 10000}
  ]
}
```

Доступные действия:
- `click`, `type`, `submit`, `scroll_to`
- `wait_for`, `wait_for_text`, `hover`
- `select_option`, `execute_js`, `upload_file`

**Особенности:**
- 🌐 JavaScript рендеринг
- 📸 Авто-скриншоты
- 🎭 Stealth mode
- 🔄 HTTP fallback при ошибках

### `search_web`

Поиск URL для последующего скрапинга.

```json
{
  "query": "golang web scraping library",
  "max_results": 5,
  "provider": "duckduckgo"
}
```

Провайдеры: `duckduckgo` (бесплатно), `brave` (требует API ключ), `bing` (требует API ключ).

### `smart_extract`

Умное извлечение контента из HTML. Используйте **ПОСЛЕ** `scrape_with_js`.

```json
{
  "html": "<html>...</html>",
  "mode": "news"
}
```

Режимы: `news`, `tech`, `finance`, `legal`, `medical`, `clean_text`, `links`.

### `parse_html`

Извлечение элементов по CSS-селекторам.

```json
{
  "html": "<html>...</html>",
  "selector": "a.link",
  "extract": "attr",
  "attribute": "href"
}
```

## 📦 Установка

### Docker (рекомендуется)

Chrome уже установлен, все работает из коробки:

```bash
docker-compose up -d
```

### Из исходников

Требуется Go 1.21+:

```bash
go build -o server ./cmd/server
./server --config config.yaml
```

### Chrome для JavaScript-рендеринга

Если не используете Docker, установите Chrome:

**Ubuntu/Debian:**
```bash
sudo apt-get install -y chromium-browser --no-install-recommends
```

**Alpine:**
```bash
apk add --no-cache chromium
```

## ⚙️ Настройка

Через переменные окружения:

| Переменная | По умолчанию |
|------------|--------------|
| `MCP_WEB_SCRAPE_SERVER_HOST` | `0.0.0.0` |
| `MCP_WEB_SCRAPE_SERVER_PORT` | `8192` |
| `MCP_WEB_SCRAPE_LOG_LEVEL` | `info` |

Или через `config.yaml` (см. пример в репозитории).

**Кеширование с TTL по типу контента:**
- HTML — 5 минут (быстро устаревает)
- JSON API — 10 минут
- CSS/JS/image — 1 час (статика)

## 🔗 Интеграция с llama.cpp WebUI

### Удаленный сервер

1. Запустите сервер: `docker-compose up -d`
2. В llama.cpp WebUI → MCP настройки добавьте:
   - **Server URL**: `https://skynet.0x27.ru/sse`
   - **Enable proxy**: ❌

### Локальный бинарник

1. Запустите: `./server --config config.yaml`
2. В llama.cpp WebUI → MCP настройки:
   - **Server URL**: `http://127.0.0.1:8192/sse`

## 📊 Производительность

**Оптимизация HTML:**
- GitHub: 310КБ → 20КБ (94% reduction)
- Новости: 130КБ → 50КБ (62% reduction)
- Блоги: 80КБ → 15КБ (81% reduction)

**Время работы:**
- HTTP scraper: 1-2 сек
- Chrome scraper: 2-6 сек
- С network idle: 5-15 сек
- С fallback: 5-10 сек при проблемах

## 🚫 Ограничения

Некоторые сайты могут блокировать скрапинг:
- **Анти-бот защита** — обходится через stealth mode
- **Блокировка по IP** — облачные IP могут быть заблокированы (нужен прокси)
- **Aggressive WAF** — некоторые магазины блокируют полностью

Решения:
- Включите `stealth_enabled: true`
- Используйте `proxy rotation`
- Попробуйте `scrape_url` вместо `scrape_with_js`

## 🏗️ Архитектура

```
UnifiedScraper (авто-выбор метода)
├── HTTPScraper (статические сайты)
│   ├── Proxy rotation
│   ├── User-Agent rotation
│   └── Smart caching
└── ChromeScraper (динамические сайты)
    ├── Browser pool
    ├── Network idle waiting
    ├── Stealth mode
    ├── Interactive actions
    └── HTTP fallback
```

**Все scrapers реализуют интерфейс:**
```go
type Scraper interface {
    Scrape(ctx, url, opts) (*Result, error)
    Name() string
    SupportsJS() bool
    SupportsActions() bool
}
```

## 📡 API-эндпоинты

- `GET /` — информация о сервере и tools
- `GET /health` — проверка здоровья
- `POST /sse` — MCP endpoint (SSE для llama.cpp)
- `POST /mcp` — MCP endpoint (JSON-RPC)
- `GET /metrics` — метрики (кеши, rate limits)

## 📝 Лицензия

MIT

---

# MCP Web Scrape Server - English Documentation

**Status:** ✅ 100% Complete | Production Ready

MCP server for web scraping with unified architecture. Works with llama.cpp WebUI, Claude Desktop, and other MCP-compatible AI agents.

## 🎯 Features

### Core Functions:
- **Dual engine** — HTTP scraper for static sites, Chrome scraper for dynamic
- **JavaScript rendering** — headless Chrome for SPAs, dashboards, GitHub
- **Interactive actions** — click, type, scroll for login-protected content
- **Markdown export** — HTML to Markdown conversion for LLMs
- **Auto-optimization** — reduces HTML by 70-95% to save tokens
- **Screenshots** — automatic for large pages (>50KB)
- **Smart caching** — caching with TTL by content type
- **HTTP fallback** — automatic switching on Chrome failures

### Advanced Functions:
- **Network Idle** — smart SPA load waiting (30 sec timeout)
- **Proxy rotation** — proxy rotation for bypassing blocks
- **User-Agent rotation** — random UAs for stealth mode
- **Browser pool** — browser reuse for performance
- **Stealth mode** — human behavior emulation (delays, scroll, mouse)
- **RAG integration** — auto-indexing for semantic search

### Architecture:
- **Unified interface** — all scrapers implement `Scraper` interface
- **Auto-selection** — `UnifiedScraper` chooses HTTP vs Chrome automatically
- **Fallback logic** — tries next scraper on error
- **Modular structure** — easy to add new scrapers

## 🛠️ MCP Tools

### `scrape_url` (HTTP scraper)

Fast HTTP scraper for **static sites**: blogs, news, documentation.

```json
{
  "url": "https://example.com/blog/post",
  "timeout": 30,
  "user_agent": "CustomBot/1.0"
}
```

**Parameters:**
- `url` (required) — URL to scrape
- `timeout` — timeout in seconds (default: 30)
- `user_agent` — custom User-Agent
- `headers` — custom HTTP headers

**Features:**
- ⚡ Fast (1-2 sec)
- 💾 Low memory usage
- 🔄 Proxy and UA rotation support
- 🎯 Optimal for static content

### `scrape_with_js` (Chrome scraper)

Universal tool for **dynamic sites**: GitHub, SPAs, dashboards.

```json
{
  "url": "https://github.com/user/repo",
  "wait_for_network_idle": true,
  "output_format": "markdown",
  "screenshot_mode": "auto"
}
```

**Parameters:**
- `url` (required) — URL to scrape
- `timeout` — timeout in seconds (default: 60)
- `wait_for` — CSS selector to wait for
- `wait_time` — delay after load in ms (default: 3000)
- `wait_for_network_idle` — smart load waiting (30 sec timeout)
- `screenshot_mode` — when to take screenshot: `"auto"`, `"always"`, `"never"`
- `output_format` — output format: `"html"` (default) or `"markdown"`
- `block_images` — block images for faster scraping
- `user_agent` — custom User-Agent
- `stealth_enabled` — enable stealth mode
- `stealth_scroll` — scroll emulation (default true)
- `stealth_mouse` — mouse movement emulation

**Interactive actions** (click, type, scroll):
```json
{
  "url": "https://example.com/login",
  "actions": [
    {"type": "type", "selector": "#username", "text": "user"},
    {"type": "type", "selector": "#password", "text": "pass"},
    {"type": "click", "selector": "button[type='submit']"},
    {"type": "wait_for_text", "text": "Welcome", "timeout": 10000}
  ]
}
```

Available actions:
- `click`, `type`, `submit`, `scroll_to`
- `wait_for`, `wait_for_text`, `hover`
- `select_option`, `execute_js`, `upload_file`

**Features:**
- 🌐 JavaScript rendering
- 📸 Auto-screenshots
- 🎭 Stealth mode
- 🔄 HTTP fallback on errors

### `search_web`

Search URLs for subsequent scraping.

```json
{
  "query": "golang web scraping library",
  "max_results": 5,
  "provider": "duckduckgo"
}
```

Providers: `duckduckgo` (free), `brave` (requires API key), `bing` (requires API key).

### `smart_extract`

Smart content extraction from HTML. Use **AFTER** `scrape_with_js`.

```json
{
  "html": "<html>...</html>",
  "mode": "news"
}
```

Modes: `news`, `tech`, `finance`, `legal`, `medical`, `clean_text`, `links`.

### `parse_html`

Extract elements by CSS selectors.

```json
{
  "html": "<html>...</html>",
  "selector": "a.link",
  "extract": "attr",
  "attribute": "href"
}
```

## 📦 Installation

### Docker (recommended)

Chrome is pre-installed, everything works out of the box:

```bash
docker-compose up -d
```

### From source

Requires Go 1.21+:

```bash
go build -o server ./cmd/server
./server --config config.yaml
```

### Chrome for JavaScript rendering

If not using Docker, install Chrome:

**Ubuntu/Debian:**
```bash
sudo apt-get install -y chromium-browser --no-install-recommends
```

**Alpine:**
```bash
apk add --no-cache chromium
```

## ⚙️ Configuration

Via environment variables:

| Variable | Default |
|----------|---------|
| `MCP_WEB_SCRAPE_SERVER_HOST` | `0.0.0.0` |
| `MCP_WEB_SCRAPE_SERVER_PORT` | `8192` |
| `MCP_WEB_SCRAPE_LOG_LEVEL` | `info` |

Or via `config.yaml` (see example in repository).

**Caching with TTL by content type:**
- HTML — 5 minutes (expires quickly)
- JSON API — 10 minutes
- CSS/JS/image — 1 hour (static)

## 🔗 Integration with llama.cpp WebUI

### Remote server

1. Start server: `docker-compose up -d`
2. In llama.cpp WebUI → MCP settings add:
   - **Server URL**: `https://skynet.0x27.ru/sse`
   - **Enable proxy**: ❌

### Local binary

1. Run: `./server --config config.yaml`
2. In llama.cpp WebUI → MCP settings:
   - **Server URL**: `http://127.0.0.1:8192/sse`

## 📊 Performance

**HTML optimization:**
- GitHub: 310KB → 20KB (94% reduction)
- News: 130KB → 50KB (62% reduction)
- Blogs: 80KB → 15KB (81% reduction)

**Execution time:**
- HTTP scraper: 1-2 sec
- Chrome scraper: 2-6 sec
- With network idle: 5-15 sec
- With fallback: 5-10 sec on issues

## 🚫 Limitations

Some sites may block scraping:
- **Anti-bot protection** — bypassed via stealth mode
- **IP-based blocking** — cloud IPs may be blocked (proxy needed)
- **Aggressive WAF** — some stores block completely

Solutions:
- Enable `stealth_enabled: true`
- Use `proxy rotation`
- Try `scrape_url` instead of `scrape_with_js`

## 🏗️ Architecture

```
UnifiedScraper (auto-selection)
├── HTTPScraper (static sites)
│   ├── Proxy rotation
│   ├── User-Agent rotation
│   └── Smart caching
└── ChromeScraper (dynamic sites)
    ├── Browser pool
    ├── Network idle waiting
    ├── Stealth mode
    ├── Interactive actions
    └── HTTP fallback
```

**All scrapers implement interface:**
```go
type Scraper interface {
    Scrape(ctx, url, opts) (*Result, error)
    Name() string
    SupportsJS() bool
    SupportsActions() bool
}
```

## 📡 API Endpoints

- `GET /` — server info and tools
- `GET /health` — health check
- `POST /sse` — MCP endpoint (SSE for llama.cpp)
- `POST /mcp` — MCP endpoint (JSON-RPC)
- `GET /metrics` — metrics (cache, rate limits)

## 📝 License

MIT