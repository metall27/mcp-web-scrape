# MCP Web Scrape Server - Полная документация

## Содержание

- [Обзор проекта](#обзор-проекта)
- [Основные возможности](#основные-возможности)
- [Стек технологий](#стек-технологий)
- [Архитектура проекта](#архитектура-проекта)
- [MCP Инструменты](#mcp-инструменты)
  - [scrape_with_js (основной инструмент)](#1-scrape_with_js-основной-инструмент)
  - [search_web](#2-search_web)
  - [smart_extract](#3-smart_extract)
  - [parse_html](#4-parse_html)
  - [RAG Tools (Semantic Search)](#5-rag-tools-semantic-search)
- [Новые возможности](#новые-возможности)
  - [Авто-оптимизация HTML](#авто-оптимизация-html)
  - [Авто-скриншоты](#авто-скриншоты)
  - [HTTP Fallback](#http-fallback)
  - [Stealth Mode](#stealth-mode)
  - [Content-Type Кеширование](#content-type-кеширование)
- [Конфигурация](#конфигурация)
- [Установка и запуск](#установка-и-запуск)
- [MCP Протокол](#mcp-протокол)
- [JavaScript Rendering](#javascript-rendering)
- [Интеграция с llama.cpp](#интеграция-с-llamacpp)
- [Разработка](#разработка)
  - [Добавление нового инструмента](#добавление-нового-инструмента)
- [Производительность и оптимизация](#производительность-и-оптимизация)
- [Ограничения](#ограничения)
- [API Endpoints](#api-endpoints)
- [Troubleshooting](#troubleshooting)

## Обзор проекта

**MCP Web Scrape Server** - сервер, реализующий протокол MCP (Model Context Protocol) для веб-скрейпинга и поиска данных из интернета. Проект разработан для интеграции с llama.cpp WebUI, Claude Desktop и другими AI-системами.

### Основные возможности

- 🌐 **Универсальный скрейпинг** — один инструмент для всех сайтов (статические и динамические)
- 🚀 **JavaScript-рендеринг** — headless Chrome для SPA, дашбордов, GitHub
- ⚡ **Авто-оптимизация** — уменьшает HTML на 70-95% для экономии токенов
- 📸 **Авто-скриншоты** — для больших страниц (>50КБ) вместо текста
- 🔄 **HTTP fallback** — автоматически переключается если Chrome не справляется
- 🕵️ **Stealth mode** — обходит базовую анти-бот защиту
- 🔍 **Умный поиск** — DuckDuckGo, Brave, Bing API
- 🎯 **Парсинг HTML** — CSS-селекторы и умное извлечение контента
- ⚡ Rate limiting и кеширование для оптимизации производительности
- 📡 Поддержка MCP протокола через HTTP/SSE transport

## Стек технологий

### Язык и фреймворки
- **Go 1.21+** - основной язык разработки
- **Gin** - HTTP фреймворк
- **zerolog** - структурированное логирование
- **viper** - управление конфигурацией

### Веб-скрейпинг
- **chromedp** - Chrome DevTools Protocol для JavaScript рендеринга
- **goquery** - jQuery-like HTML парсинг

### Кеширование и оптимизация
- **go-cache** - in-memory кеширование с поддержкой TTL по content-type
- **golang.org/x/time/rate** - rate limiting (Token Bucket algorithm)

### MCP протокол
- Собственная реализация MCP через JSON-RPC 2.0
- Поддержка HTTP POST и Server-Sent Events (SSE)

## Архитектура проекта

```
mcp-web-scrape/
├── cmd/
│   └── server/
│       └── main.go              # Точка входа, HTTP сервер
├── internal/
│   ├── mcp/                     # MCP протокол
│   │   ├── server.go            # MCP сервер, обработка запросов
│   │   ├── transport.go         # HTTP/SSE транспорт
│   │   ├── types.go             # MCP типы данных
│   │   └── tools/               # MCP инструменты
│   │       ├── tool.go          # Базовый интерфейс Tool
│   │       ├── js_tool.go       # scrape_with_js (универсальный инструмент)
│   │       ├── html_optimizer.go # Оптимизация HTML
│   │       ├── search.go        # search_web
│   │       ├── parser.go        # parse_html
│   │       └── smart_extractor.go # smart_extract
│   └── pkg/                     # Внутренние пакеты
│       ├── cache/               # Кеширование с TTL по content-type
│       ├── config/              # Конфигурация
│       └── logger/              # Логирование
├── config.yaml                  # Конфигурация по умолчанию
├── docker-compose.yml           # Docker конфигурация
├── Dockerfile                   # Multi-stage build
├── examples/                    # Примеры использования
├── go.mod/go.sum                # Go зависимости
├── README.md                    # Основная документация
├── AGENTS.md                    # Этот файл
└── DOCKER.md                    # Docker deployment guide
```

## MCP Инструменты

### 1. scrape_with_js (основной инструмент)

**Универсальный инструмент для ВСЕХ сайтов** — статических и динамических. Это единственный скрейпинг инструмент, который вам понадобится.

**Параметры:**
- `url` (string, required) - URL для скрейпинга
- `timeout` (integer, optional) - таймаут загрузки страницы в секундах (default: 60)
- `wait_for` (string, optional) - CSS селектор для ожидания элемента
- `wait_time` (integer, optional) - задержка после загрузки в мс (default: 3000)
- `screenshot_mode` (string, optional) - когда делать скриншот: "never", "auto" (default - если HTML > 50KB), "always"
- `block_images` (boolean, optional) - блокировать изображения для ускорения (default: false)
- `user_agent` (string, optional) - кастомный User-Agent
- `viewport_width` (integer, optional) - ширина viewport (default: 1920)
- `viewport_height` (integer, optional) - высота viewport (default: 1080)

**Особенности:**
- ✅ Автоматически оптимизирует HTML (удаляет скрипты, стили, навигацию)
- ✅ Для GitHub — специальная оптимизация (удаление React компонентов)
- ✅ Авто-скриншоты в base64 для vision моделей
- ✅ HTTP fallback если Chrome не справляется
- ✅ Stealth mode для обхода базовой анти-бот защиты

**Результат (MCP стандарт):**
```json
{
  "content": [
    {
      "type": "text",
      "text": "<html>...</html>"
    }
  ],
  "_metadata": {
    "url": "https://example.com",
    "final_url": "https://example.com",
    "status_code": 200,
    "content_type": "text/html",
    "size_bytes": 1256,
    "duration_ms": 1234,
    "title": "Example Domain",
    "rendering": "javascript"
  }
}
```

**Со скриншотом:**
```json
{
  "content": [
    {
      "type": "text",
      "text": "<html>...</html>"
    },
    {
      "type": "image",
      "data": "base64_encoded_image",
      "mimeType": "image/png"
    }
  ],
  "_metadata": {
    "screenshot_included": true,
    "screenshot_size": 45678
  }
}
```

### 2. search_web

Поиск URL для последующего скрейпинга.

**Параметры:**
- `query` (string, required) - поисковый запрос
- `max_results` (integer, optional) - макс. результатов (default: 10)
- `provider` (string, optional) - провайдер: duckduckgo (бесплатно), brave (требует API ключ), bing (требует API ключ)
- `safe_search` (boolean, optional) - безопасный поиск (default: true)

**Результат:**
```json
{
  "query": "golang web scraping",
  "provider": "duckduckgo",
  "total_count": 10,
  "results": [
    {
      "title": "Example",
      "url": "https://example.com",
      "snippet": "Description..."
    }
  ]
}
```

### 3. smart_extract

Умное извлечение контента из HTML. Используйте **ПОСЛЕ** `scrape_with_js`.

**Параметры:**
- `html` (string, required) - HTML контент для извлечения
- `mode` (string, optional) - режим извлечения:
  - `news` — заголовки новостей
  - `tech` — API, документация, код
  - `finance` — финансовые отчёты
  - `legal` — юридические документы
  - `medical` — медицинская информация
  - `clean_text` — чистый текст без тегов
  - `links` — все URL на странице
  - `general` — общая информация (default)
- `max_items` (integer, optional) - макс. элементов (default: 10)

**Результат:**
```json
{
  "mode": "news",
  "result": {
    "type": "news",
    "count": 5,
    "items": [
      {
        "title": "Заголовок",
        "link": "https://example.com/article"
      }
    ]
  }
}
```

### 4. parse_html

Парсинг HTML с использованием CSS селекторов.

**Параметры:**
- `html` (string, required) - HTML контент
- `selector` (string, optional) - CSS селектор (default: "*")
- `extract` (string, optional) - что извлечь: text, html, attr, all (default: "text")
- `attribute` (string, optional) - имя атрибута (обязателен при extract="attr")

**Результат:**
```json
{
  "selector": "a.link",
  "elements_found": 5,
  "extract": "attr",
  "results": [
    {
      "index": 0,
      "attribute": "href",
      "value": "https://example.com"
    }
  ]
}
```

### 5. RAG Tools (Semantic Search)

RAG (Retrieval-Augmented Generation) инструменты позволяют индексировать веб-страницы и выполнять семантический поиск в уже проиндексированном контенте.

**Когда использовать RAG инструменты:**
- 🔍 Поиск в уже проиндексированной базе знаний
- 📚 Поиск по большому количеству документов одновременно
- 🌐 Семантический поиск (понимает смысл, не только ключевые слова)
- 🔗 Работает с английским и русским языками

**Когда НЕ использовать RAG:**
- ❌ Для получения HTML с новой веб-страницы (используйте `scrape_with_js`)
- ❌ Для поиска URL в интернете (используйте `search_web`)

#### rag_search - Семантический поиск

Выполняет семантический поиск в проиндексированных документах.

**Параметры:**
- `query` (string, required) - поисковый запрос (поддерживает русский и английский)
- `top_k` (integer, optional) - количество результатов (default: 5, max: 20)
- `filters` (object, optional) - фильтры по метаданным (например, `{"url": "https://example.com"}`)

**Результат:**
```json
{
  "query": "kubernetes orchestration",
  "total_results": 3,
  "search_time_ms": 123,
  "results": [
    {
      "chunk_id": "uuid-123",
      "text": "Kubernetes is an open-source container orchestration platform...",
      "metadata": {
        "url": "https://kubernetes.io/docs/concepts/",
        "title": "Kubernetes Concepts",
        "document_id": "doc-456"
      },
      "score": 0.95
    }
  ]
}
```

**Примеры использования:**
```json
// Поиск информации о Kubernetes
{
  "name": "rag_search",
  "arguments": {
    "query": "что такое kubernetes",
    "top_k": 3
  }
}

// Поиск с фильтром по конкретному URL
{
  "name": "rag_search",
  "arguments": {
    "query": "container orchestration",
    "filters": {
      "url": "https://kubernetes.io"
    }
  }
}
```

#### rag_index - Индексация веб-страниц

Добавляет веб-страницу в базу знаний для последующего семантического поиска.

**Параметры:**
- `url` (string, required) - URL страницы для индексации
- `processing_mode` (string, optional) - режим обработки:
  - `structured` - структурированное извлечение контента (default)
  - `content` - только основное содержимое
  - `raw` - исходный HTML
- `ttl` (integer, optional) - время жизни в днях (default: 7)

**Результат:**
```json
{
  "status": "indexed",
  "document_id": "doc-789",
  "chunks_created": 15,
  "index_time_ms": 1234,
  "embeddings_model": "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2",
  "indexed_at": "2026-05-02T12:34:56Z"
}
```

**Примеры использования:**
```json
// Индексация документации Kubernetes
{
  "name": "rag_index",
  "arguments": {
    "url": "https://kubernetes.io/docs/concepts/overview/",
    "processing_mode": "structured"
  }
}

// Индексация с TTL 30 дней
{
  "name": "rag_index",
  "arguments": {
    "url": "https://example.com/docs",
    "ttl": 30
  }
}
```

#### rag_health - Проверка состояния RAG сервиса

Проверяет работоспособность RAG Research Agent.

**Параметры:** Нет

**Результат:**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime_seconds": 3600.5,
  "components": {
    "vector_store": "chromadb",
    "embeddings_model": "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2",
    "scraper": "mcp-web-scrape"
  }
}
```

#### rag_list_documents - Список проиндексированных документов

Возвращает список всех проиндексированных документов.

**Параметры:** Нет

**Результат:**
```json
{
  "documents": [
    {
      "document_id": "doc-123",
      "url": "https://kubernetes.io/docs/",
      "title": "Kubernetes Documentation",
      "chunks_count": 45,
      "indexed_at": "2026-05-02T10:00:00Z",
      "ttl": 7,
      "size_bytes": 123456
    }
  ],
  "total_count": 1
}
```

### Сравнение инструментов

| Задача | Инструмент | Пример |
|--------|-----------|--------|
| Получить HTML с новой страницы | `scrape_with_js` | scrape_with_js("https://example.com") |
| Найти URL в интернете | `search_web` | search_web("golang tutorial") |
| Искать в проиндексированной базе знаний | `rag_search` | rag_search("container orchestration") |
| Добавить страницу в базу знаний | `rag_index` | rag_index("https://docs.example.com") |
| Извлечь конкретные элементы из HTML | `parse_html` | parse_html(html, "a.link") |
| Умное извлечение контента | `smart_extract` | smart_extract(html, "news") |

### Рекомендуемый workflow

**Сценарий 1: Исследование новой темы**
```
1. Поиск релевантных источников
   → search_web("kubernetes documentation")

2. Индексация найденных страниц
   → rag_index("https://kubernetes.io/docs/")

3. Семантический поиск в проиндексированном
   → rag_search("что такое kubernetes pod")

4. Если информации недостаточно
   → scrape_with_js("https://another-source.com/docs")
```

**Сценарий 2: Работа с известными источниками**
```
1. Проверить, что уже проиндексировано
   → rag_list_documents()

2. Искать в базе знаний
   → rag_search("microservices architecture")

3. Если не найдено - индексировать новые источники
   → rag_index("https://microservices.io/patterns/")
```

**Сценарий 3: Быстрый ответ на вопрос**
```
1. Сразу попробовать поиск в RAG
   → rag_search("как работает docker")

2. Если результатов нет или мало
   → search_web("docker tutorial") → rag_index(url) → rag_search(query)
```

### Важные замечания

- **rag_search** ищет только в проиндексированных документах. Сначала нужно вызвать `rag_index`
- **RAG работает медленнее**, чем `scrape_with_js`, но позволяет искать по большому количеству документов одновременно
- **Семантический поиск** понимает смысл запроса, не только ключевые слова
- **RAG поддерживает русский и английский** языки для запросов и документов
- **TTL документов** - по умолчанию 7 дней, после чего документы удаляются

## Новые возможности

### Авто-оптимизация HTML

HTML автоматически оптимизируется для уменьшения токенов:

**Общая оптимизация:**
- Удаление `<head>`, `<script>`, `<style>`, комментариев
- Удаление навигации, хедера, футера, кнопок
- Удаление всех `<span>` тегов
- Упрощение атрибутов (data-*, aria-*, id)
- Коллапс пробелов

**GitHub-специфичная оптимизация:**
- Удаление React skeleton loaders
- Удаление `data-*` атрибутов
- Удаление кнопок действий, меню
- Удаление токенов, кнопок "Copy", reactions

**Результаты:**
- GitHub: 310КБ → 20КБ (94% reduction)
- Новости: 130КБ → 50КБ (62% reduction)
- Блоги: 80КБ → 15КБ (81% reduction)

### Авто-скриншоты

Для больших страниц (>50КБ) автоматически делается скриншот:
- Экономит токены для визуального контента
- В base64 для vision моделей
- Настраивается через `screenshot_mode`: "never", "auto", "always"

### HTTP Fallback

Если Chrome не справляется, автоматический переключение на HTTP:
- Timeout при медленных сайтах
- Ошибки Chrome
- Корректная обработка redirect

### Stealth Mode

Обход базовой анти-бот защиты:
- `exclude-switches: enable-automation`
- `disable-blink-features: AutomationControlled`
- `disable-extensions`
- `disable-background-timer-throttling`
- и другие флаги

**Ограничения:**
- Работает с базовой защитой
- IP-based блокировки (облачные IP) требуют прокси
- Aggressive WAF (DNS-shop.ru и др.) блокируют полностью

[↑ Вернуться к содержанию](#содержание)

### Content-Type Кеширование

Разный TTL по типу контента:
- HTML — 5 минут (быстро устаревает)
- JSON API — 10 минут
- CSS/JS/image — 1 час (статика)

[↑ Вернуться к содержанию](#содержание)

## Конфигурация

### Структура config.yaml

```yaml
server:
  host: 0.0.0.0                  # Хост для прослушивания
  port: 8192                      # Порт (изменено с 8080)
  read_timeout: 30s               # Таймаут чтения
  write_timeout: 30s              # Таймаут записи

mcp:
  endpoint: /mcp                  # MCP endpoint
  sse_endpoint: /sse              # SSE endpoint для llama.cpp

scraping:
  user_agent: "MCP-Web-Scrape/1.0"
  timeout: 60s                    # Увеличен с 30s
  wait_time: 3000ms               # Увеличен с 1000ms
  max_redirects: 10
  max_body_size: 10485760         # 10MB

browser:
  enabled: true                   # Включить headless Chrome
  timeout: 60s
  wait_time: 3s
  viewport_width: 1920
  viewport_height: 1080
  headless: true
  block_images: false
  stealth_mode: true              # Stealth mode включен

search:
  provider: duckduckgo            # duckduckgo, brave, bing
  api_key: ""                     # Требуется для brave/bing
  max_results: 10
  safe_search: true

rate_limit:
  enabled: true
  requests_per_second: 10.0
  burst_size: 20

cache:
  enabled: true
  # TTL по content-type
  ttl_html: 5m                    # HTML устаревает быстро
  ttl_json: 10m                   # JSON API
  ttl_static: 1h                  # CSS/JS/image
  cleanup_interval: 10m

log:
  level: info                     # debug, info, warn, error
  pretty: true                    # Красивый вывод
```

### Переменные окружения

Все настройки можно переопределить через переменные окружения с префиксом `MCP_WEB_SCRAPE_`:

```bash
export MCP_WEB_SCRAPE_SERVER_PORT=8192
export MCP_WEB_SCRAPE_LOG_LEVEL=debug
export MCP_WEB_SCRAPE_BROWSER_ENABLED=true
```

## Установка и запуск

### Требования
- Go 1.21 или выше
- (Опционально) Chrome/Chromium для JavaScript рендеринга

### Сборка

```bash
# Клонирование репозитория
git clone https://github.com/metall/mcp-web-scrape.git
cd mcp-web-scrape

# Скачивание зависимостей
go mod download

# Сборка
go build -o mcp-web-scrape ./cmd/server
```

### Запуск

```bash
# Запуск с дефолтной конфигурацией
./mcp-web-scrape

# Запуск с кастомной конфигурацией
./mcp-web-scrape -config /path/to/config.yaml

# Запуск через переменные окружения
MCP_WEB_SCRAPE_SERVER_PORT=8192 ./mcp-web-scrape
```

[↑ Вернуться к содержанию](#содержание)

### Docker (рекомендуется)

Chrome уже установлен, все работает из коробки:

```bash
docker-compose up -d
```

## MCP Протокол

### Транспорт

Сервер поддерживает два режима транспорта:

1. **HTTP POST** - классический JSON-RPC через HTTP (`/mcp`)
2. **Server-Sent Events (SSE)** - streaming для llama.cpp (`/sse`)

### Структура запроса

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "scrape_with_js",
    "arguments": {
      "url": "https://github.com/user/repo"
    }
  }
}
```

### Поддерживаемые методы

- `initialize` - инициализация соединения
- `tools/list` - список доступных инструментов
- `tools/call` - вызов инструмента
- `resources/list` - список ресурсов (пустой)
- `prompts/list` - список промптов (пустой)
- `ping` - проверка соединения

### CORS поддержка

Сервер поддерживает CORS для браузерных клиентов:

```http
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET, POST, OPTIONS
Access-Control-Allow-Headers: Content-Type, Authorization, X-API-Key
```

## JavaScript Rendering

### Техническая реализация

JavaScript рендеринг реализован через библиотеку **chromedp**, которая использует Chrome DevTools Protocol:

**Компоненты:**
- **ExecAllocator** - управляет процессами Chrome
- **Context** - управляет жизненным циклом браузера
- **Tasks** - цепочка действий для выполнения

**Stealth Mode флаги:**
- `exclude-switches: enable-automation` - скрывает автоматизацию
- `disable-blink-features: AutomationControlled` - отключает детектор
- `disable-extensions` - отключает расширения
- `disable-background-timer-throttling` - предотвращает throttling
- и другие флаги для реалистичности

**Оптимизации:**
- `NoSandbox` - отключение песокницы (для Docker)
- `DisableGPU` - отключение GPU (для headless режима)
- `Headless` - запуск без графического интерфейса
- `BlockImages` - возможность блокировки изображений

### Пример использования

```json
{
  "name": "scrape_with_js",
  "arguments": {
    "url": "https://react.example.com",
    "wait_for": ".app-loaded",
    "wait_time": 2000,
    "screenshot_mode": "auto"
  }
}
```

### HTTP Fallback

Автоматическое переключение на HTTP при:
- Chrome timeout (например, rt.rbc.ru)
- Chrome crash
- Ошибки рендеринга

**Логика:**
1. Попытка рендеринга через Chrome
2. При ошибке — HTTP запрос с реалистичными headers
3. Возврат оптимизированного HTML

[↑ Вернуться к содержанию](#содержание)

## Интеграция с llama.cpp WebUI

### Настройка в WebUI

1. Откройте llama.cpp WebUI
2. Перейдите в настройки MCP
3. Добавьте новый сервер:
   - **Server URL**: `http://127.0.0.1:8192/sse` (или `https://skynet.0x27.ru/sse`)
   - **Enable proxy**: ❌ (нужен только для stdio MCP)

### Удаленный сервер

Для удаленного MCP сервера:
- **Server URL**: `https://skynet.0x27.ru/sse`
- **Enable proxy**: ❌

## Разработка

### Добавление нового инструмента

1. Создайте файл в `internal/mcp/tools/`:

```go
package tools

type MyTool struct {
    *BaseTool
}

func NewMyTool() *MyTool {
    schema := map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "param": map[string]interface{}{
                "type": "string",
                "description": "Description",
            },
        },
        "required": []string{"param"},
    }

    tool := &MyTool{}
    tool.BaseTool = NewBaseTool(
        "my_tool",
        "Tool description",
        schema,
        tool.Execute,
    )
    return tool
}

func (t *MyTool) Execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
    // Реализация
    return map[string]interface{}{
        "result": "success",
    }, nil
}
```

2. Зарегистрируйте в `internal/mcp/server.go`:

```go
func (s *Server) registerDefaultTools() error {
    defaultTools := []tools.Tool{
        tools.NewScrapeJSTool(),      // Универсальный инструмент
        tools.NewSearchTool(),         // Поиск
        tools.NewParseHTMLTool(),      // Парсинг
        tools.NewSmartExtractorTool(), // Извлечение
        tools.NewMyTool(),             // ← Новый инструмент
    }
    // ...
}
```

[↑ Вернуться к содержанию](#содержание)

## Производительность и оптимизация

### Rate Limiting

- **Token Bucket алгоритм** - 10 запросов/сек, burst 20
- Настраивается через `rate_limit.requests_per_second`
- Предотвращает перегрузку сервера

### Кеширование

- **Content-type aware TTL**:
  - HTML: 5 минут (быстро устаревает)
  - JSON API: 10 минут
  - CSS/JS/image: 1 час (статика)
- Автоматическая очистка каждые 10 минут

### Оптимизации браузера

- **Блокировка изображений** - ускоряет загрузку на 50-70%
- **Оптимизация viewport** - уменьшает расход памяти
- **Reusable contexts** - переиспользование процессов Chrome
- **Stealth mode** - предотвращает детекцию как бота

### Производительность

**Время работы:**
- Статические страницы: 1-2 сек
- JavaScript-сайты: 2-6 сек
- С fallback: 5-10 сек при проблемах с Chrome

**Оптимизация HTML:**
- GitHub: 310КБ → 20КБ (94% reduction)
- Новости: 130КБ → 50КБ (62% reduction)
- Блоги: 80КБ → 15КБ (81% reduction)

[↑ Вернуться к содержанию](#содержание)

## Ограничения

Некоторые сайты могут блокировать скрапинг:
- **Анти-бот защита** — работают через stealth mode
- **Блокировка по IP** — облачные IP могут быть заблокированы (нужен прокси)
- **Aggressive WAF** — некоторые магазины (DNS-shop.ru и др.) блокируют полностью

Для таких сайтов используйте `search_web` или альтернативные источники.

## API Endpoints

### Основные

- `GET /` - информация о сервере и инструментах
- `GET /health` - health check
- `GET /metrics` - метрики (rate limit, cache)
- `POST /mcp` - MCP endpoint (JSON-RPC)
- `POST /sse` - MCP endpoint (SSE для llama.cpp)

### Примеры запросов

```bash
# Информация о сервере
curl http://localhost:8192/ | jq

# Health check
curl http://localhost:8192/health | jq

# Метрики
curl http://localhost:8192/metrics | jq

# MCP tools/list
curl -X POST http://localhost:8192/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq

# MCP scrape_with_js
curl -X POST http://localhost:8192/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "scrape_with_js",
      "arguments": {
        "url": "https://example.com"
      }
    }
  }' | jq
```

[↑ Вернуться к содержанию](#содержание)

## Troubleshooting

### Проблемы с Chrome

```bash
# Проверка установки Chrome
which chromium
which google-chrome

# Отладка chromedp
MCP_WEB_SCRAPE_LOG_LEVEL=debug ./mcp-web-scrape
```

### Проблемы с портами

```bash
# Проверка занятости порта
lsof -i :8192

# Использование другого порта
MCP_WEB_SCRAPE_SERVER_PORT=9090 ./mcp-web-scrape
```

### Проблемы с MCP

```bash
# Проверка MCP endpoint
curl -v http://localhost:8192/mcp

# Проверка SSE endpoint
curl -v http://localhost:8192/sse

# Проверка инструментов
curl -X POST http://localhost:8192/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

## Лицензия

MIT License - свободно используйте в коммерческих и некоммерческих проектах.

## Контакты и поддержка

- GitHub: https://github.com/metall/mcp-web-scrape
- Issues: https://github.com/metall/mcp-web-scrape/issues

## Благодарности

- **llama.cpp** - отличная платформа для AI инференса
- **Model Context Protocol** - стандарт для AI tool integration
- **Go community** - отличные библиотеки для веб-скрейпинга

---

**Версия документа:** 2.0.0
**Последнее обновление:** 2026-05-02
**Статус:** Production Ready ✅
