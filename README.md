# MCP Web Scrape Server

MCP-сервер для веб-скрапинга и поиска. Работает с llama.cpp WebUI.

## Возможности

- **Веб-скрапинг** — загрузка контента по URL
- **JavaScript-рендеринг** — поддержка динамического контента через headless Chrome
- **Поиск** — DuckDuckGo (бесплатно), Brave Search API, Bing Search API
- **Парсинг HTML** — извлечение элементов по CSS-селекторам
- **Docker** — готовый образ с предустановленным Chrome

## MCP-инструменты

### `scrape_url`

Базовый HTTP-скрапинг без JavaScript.

```json
{
  "url": "https://example.com",
  "timeout": 30
}
```

### `scrape_with_js`

Загрузка с JavaScript-рендерингом через headless Chrome.

```json
{
  "url": "https://react.example.com",
  "wait_for": ".app-loaded",
  "screenshot": true,
  "block_images": true
}
```

### `search_web`

Поиск в интернете.

```json
{
  "query": "golang web scraping",
  "max_results": 5,
  "provider": "duckduckgo"
}
```

### `parse_html`

Парсинг HTML и извлечение элементов.

```json
{
  "html": "<html>...</html>",
  "selector": "a.link",
  "extract": "attr",
  "attribute": "href"
}
```

## Установка

### Docker (рекомендуется)

Самый простой способ — Chrome уже установлен:

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

**Chrome Headless Shell:**
```bash
wget https://storage.googleapis.com/chrome-for-testing/public/123.0.6312.58/linux64/chrome-headless-shell-linux64.zip
unzip chrome-headless-shell-linux64.zip
sudo mv chrome-headless-shell-linux64/chrome-headless-shell /usr/local/bin/
```

## Настройка

Через переменные окружения:

| Переменная | По умолчанию |
|------------|--------------|
| `MCP_WEB_SCRAPE_SERVER_HOST` | `0.0.0.0` |
| `MCP_WEB_SCRAPE_SERVER_PORT` | `8080` |
| `MCP_WEB_SCRAPE_LOG_LEVEL` | `info` |

Или через файл `config.yaml`.

## Интеграция с llama.cpp WebUI

### Docker

1. Запустите сервер: `docker-compose up -d`
2. В llama.cpp WebUI → MCP настройки добавьте:
   - **URL**: `http://host.docker.internal:8080/mcp`
   - **Enable proxy**: ✅

### Локальный бинарник

1. Запустите: `./mcp-web-scrape`
2. В llama.cpp WebUI → MCP настройки добавьте:
   - **URL**: `http://127.0.0.1:8080/mcp`
   - **Enable proxy**: ✅

## API-эндпоинты

- `GET /` — информация о сервере
- `GET /health` — проверка здоровья
- `ANY /mcp` — MCP-эндпоинт
- `GET /metrics` — метрики

## Лицензия

MIT
