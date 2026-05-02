# MCP Web Scrape Server

MCP-сервер для веб-скрапинга. Работает с llama.cpp WebUI, Claude Desktop и другими MCP-совместимыми AI агентами.

## Содержание

- [Возможности](#возможности)
- [MCP-инструменты](#mcp-инструменты)
  - [scrape_with_js (основной)](#scrape_with_js-основной)
  - [search_web](#search_web)
  - [smart_extract](#smart_extract)
  - [parse_html](#parse_html)
- [Результаты скрапинга](#результаты-скрапинга)
- [Установка](#установка)
  - [Docker (рекомендуется)](#docker-рекомендуется)
  - [Из исходников](#из-исходников)
  - [Chrome для JavaScript-рендеринга](#chrome-для-javascript-рендеринга)
- [Настройка](#настройка)
- [Интеграция с llama.cpp WebUI](#интеграция-с-llamacpp-webui)
- [Ограничения](#ограничения)
- [Производительность](#производительность)
- [API-эндпоинты](#api-эндпоинты)

## Возможности

- **Универсальный скрапинг** — один инструмент для всех сайтов (статические и динамические)
- **JavaScript-рендеринг** — headless Chrome для SPA, дашбордов, GitHub
- **Авто-оптимизация** — уменьшает HTML на 70-95% для экономии токенов
- **Авто-скриншоты** — для больших страниц (>50КБ) вместо текста
- **HTTP fallback** — автоматически переключается если Chrome не справляется
- **Stealth mode** — обходит базовую анти-бот защиту
- **Умный поиск** — DuckDuckGo, Brave, Bing API
- **Парсинг HTML** — CSS-селекторы и умное извлечение контента

## MCP-инструменты

### `scrape_with_js` (основной)

Универсальный инструмент для ВСЕХ сайтов — статических и динамических.

```json
{
  "url": "https://github.com/user/repo",
  "screenshot_mode": "auto"
}
```

**Параметры:**
- `url` (обязательный) — URL для скрапинга
- `timeout` — таймаут в секундах (по умолчанию 60)
- `wait_time` — задержка после загрузки в мс (по умолчанию 3000)
- `screenshot_mode` — когда делать скриншот:
  - `"auto"` (по умолчанию) — если HTML > 50КБ
  - `"always"` — всегда
  - `"never"` — никогда
- `block_images` — блокировать картинки для ускорения
- `user_agent` — кастомный User-Agent

**Особенности:**
- Автоматически оптимизирует HTML (удаляет скрипты, стили, навигацию)
- Для GitHub — специальная оптимизация
- Скриншоты в base64 для vision моделей
- HTTP fallback если Chrome не справляется
- Stealth mode для обхода защиты

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

Режимы:
- `news` — заголовки новостей
- `tech` — API, документация, код
- `finance` — финансовые отчёты
- `legal` — юридические документы
- `medical` — медицинская информация
- `clean_text` — чистый текст без тегов
- `links` — все URL на странице

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

## Результаты скрапинга

**Формат ответа (MCP стандарт):**
```json
{
  "content": [
    {
      "type": "text",
      "text": "HTML контент..."
    }
  ],
  "_metadata": {
    "url": "https://example.com",
    "status_code": 200,
    "size_bytes": 20480,
    "duration_ms": 1234
  }
}
```

**Со скриншотом:**
```json
{
  "content": [
    {
      "type": "text",
      "text": "HTML контент..."
    },
    {
      "type": "image",
      "data": "base64...",
      "mimeType": "image/png"
    }
  ]
}
```

## Установка

### Docker (рекомендуется)

Chrome уже установлен, все работает из коробки:

```bash
docker-compose up -d
```

### Из исходников

Требуется Go 1.21+:

```bash
go build -o mcp-web-scrape ./cmd/server
./mcp-web-scrape
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

## Настройка

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

## Интеграция с llama.cpp WebUI

1. Запустите сервер: `docker-compose up -d`
2. В llama.cpp WebUI → MCP настройки добавьте:
   - **Server URL**: `https://skynet.0x27.ru/sse`
   - **Enable proxy**: ❌ (нужен только для stdio MCP)

### Локальный бинарник

1. Запустите: `./mcp-web-scrape`
2. В llama.cpp WebUI → MCP настройки:
   - **Server URL**: `http://127.0.0.1:8192/sse`

## Ограничения

Некоторые сайты могут блокировать скрапинг:
- **Анти-бот защита** — работают через stealth mode
- **Блокировка по IP** — облачные IP могут быть заблокированы (нужен прокси)
- **Aggressive WAF** — некоторые магазины (DNS-shop.ru и др.) блокируют полностью

Для таких сайтов используйте `search_web` или альтернативные источники.

## Производительность

**Оптимизация HTML:**
- GitHub: 310КБ → 20КБ (94% reduction)
- Новости: 130КБ → 50КБ (62% reduction)
- Блоги: 80КБ → 15КБ (81% reduction)

**Время работы:**
- Статические страницы: 1-2 сек
- JavaScript-сайты: 2-6 сек
- Сfallback: 5-10 сек при проблемах с Chrome

## API-эндпоинты

- `GET /` — информация о сервере и tools
- `GET /health` — проверка здоровья
- `POST /sse` — MCP endpoint (SSE для llama.cpp)
- `POST /mcp` — MCP endpoint (JSON-RPC)
- `GET /metrics` — метрики (кеши, rate limits)

[↑ Вернуться к содержанию](#содержание)

## Лицензия

MIT

---

# MCP Web Scrape Server - English Documentation

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [MCP Tools](#mcp-tools)
  - [scrape_with_js (primary)](#scrape_with_js-primary)
  - [search_web](#search_web)
  - [smart_extract](#smart_extract)
  - [parse_html](#parse_html)
- [Scrape Results](#scrape-results)
- [Installation](#installation)
  - [Docker (recommended)](#docker-recommended)
  - [From source](#from-source)
  - [Chrome for JavaScript rendering](#chrome-for-javascript-rendering)
- [Configuration](#configuration)
- [Integration with llama.cpp WebUI](#integration-with-llamacpp-webui)
- [Limitations](#limitations)
- [Performance](#performance)
- [API Endpoints](#api-endpoints)

## Overview

**MCP Web Scrape Server** is a server implementing the MCP (Model Context Protocol) for web scraping and data retrieval from the internet. The project is designed for integration with llama.cpp WebUI, Claude Desktop, and other AI-compatible agents.

## Features

- **Universal scraping** — one tool for all websites (static and dynamic)
- **JavaScript rendering** — headless Chrome for SPAs, dashboards, GitHub
- **Auto-optimization** — reduces HTML by 70-95% to save tokens
- **Auto-screenshots** — for large pages (>50KB) instead of text
- **HTTP fallback** — automatically switches if Chrome fails
- **Stealth mode** — bypasses basic anti-bot protection
- **Smart search** — DuckDuckGo, Brave, Bing API
- **HTML parsing** — CSS selectors and smart content extraction

## MCP Tools

### `scrape_with_js` (primary)

Universal tool for ALL websites — static and dynamic.

```json
{
  "url": "https://github.com/user/repo",
  "screenshot_mode": "auto"
}
```

**Parameters:**
- `url` (required) — URL to scrape
- `timeout` — timeout in seconds (default: 60)
- `wait_time` — delay after load in ms (default: 3000)
- `screenshot_mode` — when to take screenshot:
  - `"auto"` (default) — if HTML > 50KB
  - `"always"` — always
  - `"never"` — never
- `block_images` — block images for faster scraping
- `user_agent` — custom User-Agent

**Features:**
- Automatically optimizes HTML (removes scripts, styles, navigation)
- GitHub-specific optimization
- Screenshots in base64 for vision models
- HTTP fallback if Chrome fails
- Stealth mode for bypassing protection

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

Modes:
- `news` — news headlines
- `tech` — API, documentation, code
- `finance` — financial reports
- `legal` — legal documents
- `medical` — medical information
- `clean_text` — plain text without tags
- `links` — all URLs on page

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

## Scrape Results

**Response format (MCP standard):**
```json
{
  "content": [
    {
      "type": "text",
      "text": "HTML content..."
    }
  ],
  "_metadata": {
    "url": "https://example.com",
    "status_code": 200,
    "size_bytes": 20480,
    "duration_ms": 1234
  }
}
```

**With screenshot:**
```json
{
  "content": [
    {
      "type": "text",
      "text": "HTML content..."
    },
    {
      "type": "image",
      "data": "base64...",
      "mimeType": "image/png"
    }
  ]
}
```

## Installation

### Docker (recommended)

Chrome is pre-installed, everything works out of the box:

```bash
docker-compose up -d
```

### From source

Requires Go 1.21+:

```bash
go build -o mcp-web-scrape ./cmd/server
./mcp-web-scrape
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

## Configuration

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

## Integration with llama.cpp WebUI

1. Start server: `docker-compose up -d`
2. In llama.cpp WebUI → MCP settings add:
   - **Server URL**: `https://skynet.0x27.ru/sse`
   - **Enable proxy**: ❌ (only needed for stdio MCP)

### Local binary

1. Run: `./mcp-web-scrape`
2. In llama.cpp WebUI → MCP settings:
   - **Server URL**: `http://127.0.0.1:8192/sse`

## Limitations

Some sites may block scraping:
- **Anti-bot protection** — works through stealth mode
- **IP-based blocking** — cloud IPs may be blocked (proxy needed)
- **Aggressive WAF** — some stores (DNS-shop.ru, etc.) block completely

For such sites, use `search_web` or alternative sources.

## Performance

**HTML optimization:**
- GitHub: 310KB → 20KB (94% reduction)
- News: 130KB → 50KB (62% reduction)
- Blogs: 80KB → 15KB (81% reduction)

**Execution time:**
- Static pages: 1-2 sec
- JavaScript sites: 2-6 sec
- With fallback: 5-10 sec when Chrome has issues

## API Endpoints

- `GET /` — server info and tools
- `GET /health` — health check
- `POST /sse` — MCP endpoint (SSE for llama.cpp)
- `POST /mcp` — MCP endpoint (JSON-RPC)
- `GET /metrics` — metrics (cache, rate limits)

[↑ Вернуться to contents](#table-of-contents)

## License

MIT
