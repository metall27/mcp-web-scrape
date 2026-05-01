# MCP Web Scrape Server

MCP-сервер для веб-скрапинга. Работает с llama.cpp WebUI, Claude Desktop и другими MCP-совместимыми AI агентами.

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

## Лицензия

MIT
