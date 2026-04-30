# MCP Web Scrape Server - Полная документация

## Обзор проекта

**MCP Web Scrape Server** - сервер, реализующий протокол MCP (Model Context Protocol) для веб-скрейпинга и поиска данных из интернета. Проект разработан для интеграции с llama.cpp и другими AI-системами.

### Основные возможности

- 🌐 Веб-скрейпинг с поддержкой HTTP и JavaScript рендеринга
- 🔍 Поиск информации через DuckDuckGo (бесплатно), Brave Search API, Bing Search API
- 🎯 Парсинг HTML с использованием CSS селекторов
- ⚡ Rate limiting и кеширование для оптимизации производительности
- 🔄 Поддержка MCP протокола через HTTP/SSE transport
- 🖥️ Headless Chrome для рендеринга JavaScript-сайтов
- 📸 Возможность создания скриншотов страниц

## Стек технологий

### Язык и фреймворки
- **Go 1.21+** - основной язык разработки
- **Gin** - HTTP фреймворк
- **zerolog** - структурированное логирование
- **viper** - управление конфигурацией

### Веб-скрейпинг
- **colly** - мощный scraping фреймворк
- **goquery** - jQuery-like HTML парсинг
- **chromedp** - Chrome DevTools Protocol для JavaScript рендеринга

### Кеширование и оптимизация
- **go-cache** - in-memory кеширование
- **golang.org/x/time/rate** - rate limiting

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
│   │       ├── scraper.go       # scrape_url (HTTP)
│   │       ├── js_tool.go       # scrape_with_js (Chrome)
│   │       ├── search.go        # search_web
│   │       └── parser.go        # parse_html
│   └── pkg/                     # Внутренние пакеты
│       ├── cache/               # Кеширование
│       ├── config/              # Конфигурация
│       └── logger/              # Логирование
├── config.yaml                  # Конфигурация по умолчанию
├── examples/                    # Примеры использования
├── go.mod/go.sum                # Go зависимости
├── README.md                    # Основная документация
└── AGENTS.md                    # Этот файл
```

## MCP Инструменты

### 1. scrape_url
Базовый HTTP скрейпинг без JavaScript рендеринга.

**Параметры:**
- `url` (string, required) - URL для скрейпинга
- `timeout` (integer, optional) - таймаут в секундах (default: 30)
- `user_agent` (string, optional) - кастомный User-Agent
- `headers` (object, optional) - кастомные HTTP заголовки

**Результат:**
```json
{
  "url": "https://example.com",
  "status_code": 200,
  "content_type": "text/html",
  "content": "<html>...</html>",
  "size_bytes": 1256,
  "duration_ms": 245,
  "title": "Example Domain",
  "headers": {...}
}
```

### 2. scrape_with_js
Продвинутый скрейпинг с JavaScript рендерингом через headless Chrome.

**Параметры:**
- `url` (string, required) - URL для скрейпинга
- `timeout` (integer, optional) - таймаут загрузки страницы (default: 30)
- `wait_for` (string, optional) - CSS селектор для ожидания элемента
- `wait_time` (integer, optional) - дополнительное ожидание в мс (default: 1000)
- `screenshot` (boolean, optional) - создать скриншот (default: false)
- `user_agent` (string, optional) - кастомный User-Agent
- `viewport_width` (integer, optional) - ширина viewport (default: 1920)
- `viewport_height` (integer, optional) - высота viewport (default: 1080)
- `block_images` (boolean, optional) - блокировать изображения (default: false)

**Результат:**
```json
{
  "url": "https://example.com",
  "final_url": "https://example.com",
  "status_code": 200,
  "content_type": "text/html",
  "content": "<html>...</html>",
  "size_bytes": 1256,
  "duration_ms": 1234,
  "title": "Example Domain",
  "rendering": "javascript",
  "screenshot": "base64_encoded_image",
  "screenshot_size": 45678
}
```

### 3. search_web
Поиск информации в интернете.

**Параметры:**
- `query` (string, required) - поисковый запрос
- `max_results` (integer, optional) - макс. результатов (default: 10)
- `provider` (string, optional) - провайдер: duckduckgo, brave, bing (default: duckduckgo)
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

### 4. parse_html
Парсинг HTML с использованием CSS селекторов.

**Параметры:**
- `html` (string, required) - HTML контент
- `selector` (string, optional) - CSS селектор (default: "*")
- `extract` (string, optional) - что извлечь: text, html, attr, all (default: "text")
- `attribute` (string, optional) - имя атрибута (обязателен при extract="attr")
- `metadata` (boolean, optional) - включить метаданные (default: false)

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
  ],
  "metadata": {...}
}
```

## Конфигурация

### Структура config.yaml

```yaml
server:
  host: 0.0.0.0                  # Хост для прослушивания
  port: 8080                      # Порт
  read_timeout: 30s               # Таймаут чтения
  write_timeout: 30s              # Таймаут записи

mcp:
  endpoint: /mcp                  # MCP endpoint
  api_key_header: X-API-Key       # Заголовок для API ключа

scraping:
  user_agent: "MCP-Web-Scrape/1.0"
  timeout: 30s
  max_redirects: 10
  max_body_size: 10485760         # 10MB
  allowed_domains: []             # Пустой = все домены

browser:
  enabled: true                   # Включить headless Chrome
  timeout: 30s
  wait_time: 1s
  viewport_width: 1920
  viewport_height: 1080
  headless: true
  block_images: false
  disable_gpu: true
  no_sandbox: true

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
  ttl: 5m                         # Время жизни кеша
  cleanup_interval: 10m           # Интервал очистки

log:
  level: info                     # debug, info, warn, error
  pretty: true                    # Красивый вывод
```

### Переменные окружения

Все настройки можно переопределить через переменные окружения с префиксом `MCP_WEB_SCRAPE_`:

```bash
export MCP_WEB_SCRAPE_SERVER_PORT=9090
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
MCP_WEB_SCRAPE_SERVER_PORT=9090 ./mcp-web-scrape
```

### Docker (будущая функциональность)

```dockerfile
# Пример Dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o mcp-web-scrape ./cmd/server

FROM alpine:latest
RUN apk --no-cache add chromium
COPY --from=builder /app/mcp-web-scrape /usr/local/bin/
CMD ["mcp-web-scrape"]
```

## MCP Протокол

### Транспорт

Сервер поддерживает три режима транспорта:

1. **HTTP POST** - классический JSON-RPC через HTTP
2. **Server-Sent Events (SSE)** - streaming для real-time обновлений
3. **StreamableHTTP** - современный HTTP streaming (будет добавлен)

### Структура запроса

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "scrape_url",
    "arguments": {
      "url": "https://example.com"
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

**Оптимизации:**
- `NoSandbox` - отключение песочницы (для Docker)
- `DisableGPU` - отключение GPU (для headless режима)
- `Headless` - запуск без графического интерфейса
- `BlockSize` - возможность блокировки изображений

### Пример использования

```json
{
  "name": "scrape_with_js",
  "arguments": {
    "url": "https://react.example.com",
    "wait_for": ".app-loaded",
    "wait_time": 2000,
    "screenshot": true,
    "viewport_width": 1920,
    "viewport_height": 1080
  }
}
```

### Ограничения

- Требует установленного Chrome/Chromium
- Больше потребляет памяти, чем HTTP скрейпинг
- Медленнее для простых страниц
- Нужна дополнительная настройка в Docker

### Установка Chrome/Chromium на Linux сервере

Для работы `scrape_with_js` необходим Chrome или Chromium. **Важно:** Используйте варианты без GUI зависимостей для серверных установок.

#### Ubuntu/Debian (без GUI)

```bash
# Вариант 1: Chromium без рекомендованных зависимостей
sudo apt-get update
sudo apt-get install -y chromium-browser --no-install-recommends

# Вариант 2: Минимальная установка
sudo apt-get install -y chromium-codecs-ffmpeg-extra

# Проверка установки
chromium-browser --version
chromium-browser --headless --no-sandbox --dump-dom https://example.com
```

#### CentOS/RHEL/Rocky

```bash
# Chromium (минимальные зависимости)
sudo yum install -y chromium

# Проверка GUI зависимостей
yum deplist chromium | grep -E "x11|gtk|qt"
```

#### Chrome Headless Shell (самый чистый вариант)

Google предлагает специальную версию для headless режима:

```bash
# Скачивание
wget https://storage.googleapis.com/chrome-for-testing/public/123.0.6312.58/linux64/chrome-headless-shell-linux64.zip
unzip chrome-headless-shell-linux64.zip
sudo mv chrome-headless-shell-linux64/chrome-headless-shell /usr/local/bin/

# Тест
chrome-headless-shell --dump-dom https://example.com
```

#### Сравнение вариантов

| Вариант | GUI зависимости | Размер | Стабильность | Для продакшена |
|---------|----------------|--------|--------------|----------------|
| `google-chrome` | ❌ Есть | ~400MB | ⭐⭐⭐⭐⭐ | ❌ |
| `chromium-browser` | ⚠️ Минимальные | ~350MB | ⭐⭐⭐⭐ | ⚠️ |
| `chromium` (Alpine) | ✅ Нет GUI | ~150MB | ⭐⭐⭐⭐ | ✅ |
| `chrome-headless-shell` | ✅ Нет GUI | ~100MB | ⭐⭐⭐ | ✅ |
| Docker Alpine | ✅ Нет GUI | ~300MB | ⭐⭐⭐⭐⭐ | ✅✅ |

#### Проверка отсутствия GUI зависимостей

```bash
# Ubuntu/Debian
apt-cache depends chromium-browser | grep -E "x11|gtk|qt"
dpkg -l | grep -E "x11|gtk|qt|libx11"

# CentOS/RHEL
yum deplist chromium | grep -E "x11|gtk|qt"
rpm -qa | grep -E "x11|gtk|qt"
```

## Docker部署

Docker является **рекомендуемым** способом запуска MCP сервера с JavaScript rendering. Он полностью изолирует Chrome и не требует установки GUI библиотек.

### Dockerfile (Multi-stage build)

```dockerfile
# Stage 1: Build
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
# Скачать зависимости
RUN go mod download
# Собрать бинарник
RUN CGO_ENABLED=0 go build -o mcp-web-scrape ./cmd/server

# Stage 2: Runtime
FROM alpine:latest
# Установить Chromium БЕЗ GUI зависимостей
RUN apk add --no-cache chromium \
    && rm -rf /var/cache/apk/* \
    /var/tmp/* \
    /tmp/*

# Создать пользователя (без root)
RUN addgroup -g 1000 -S mcp && \
    adduser -u 1000 -S mcp -G mcp

WORKDIR /app
# Скопировать бинарник из builder stage
COPY --from=builder /app/mcp-web-scrape /app/
COPY config.yaml /app/

# Изменить владельца
RUN chown -R mcp:mcp /app

# Переключиться на пользователя
USER mcp

# Порты
# 8080 - HTTP/MCP server
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Переменные окружения
ENV MCP_WEB_SCRAPE_SERVER_HOST=0.0.0.0
ENV MCP_WEB_SCRAPE_SERVER_PORT=8080
ENV MCP_WEB_SCRAPE_BROWSER_ENABLED=true

CMD ["./mcp-web-scrape"]
```

### docker-compose.yml

```yaml
version: '3.8'

services:
  mcp-web-scrape:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: mcp-web-scrape
    restart: unless-stopped
    ports:
      - "8080:8080"    # MCP endpoint
    environment:
      - MCP_WEB_SCRAPE_LOG_LEVEL=info
      - MCP_WEB_SCRAPE_BROWSER_ENABLED=true
      - MCP_WEB_SCRAPE_RATE_LIMIT_ENABLED=true
    volumes:
      # Монтировать конфиг (опционально)
      - ./config.yaml:/app/config.yaml:ro
      # Для persisted cache (будущая функциональность)
      - cache-data:/app/cache
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health"]
      interval: 30s
      timeout: 3s
      retries: 3
      start_period: 5s
    # Resource limits (важно для Chrome)
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 2G
        reservations:
          cpus: '0.5'
          memory: 512M
    networks:
      - mcp-network

volumes:
  cache-data:

networks:
  mcp-network:
    driver: bridge
```

### Команды Docker

```bash
# Сборка образа
docker build -t mcp-web-scrape:latest .

# Запуск через docker-compose
docker-compose up -d

# Проверка логов
docker-compose logs -f

# Проверка health status
docker-compose ps
docker inspect mcp-web-scrape | grep -A 10 Health

# Остановка
docker-compose down

# Пересборка
docker-compose up -d --build
```

### Docker порты и endpoints

| Порт | Назначение | Внутри контейнера | Снаружи | Пример использования |
|------|------------|-------------------|---------|---------------------|
| 8080 | HTTP/MCP Server | 8080 | 8080 | `http://localhost:8080/mcp` |

### Оптимизация Docker образа

```dockerfile
# Для ещё меньшего размера образа
FROM alpine:latest AS chrome-downloader
# Скачивание только headless Chrome
RUN apk add --no-cache wget && \
    wget https://storage.googleapis.com/chrome-for-testing/public/123.0.6312.58/linux64/chrome-headless-shell-linux64.zip && \
    unzip chrome-headless-shell-linux64.zip -d /opt/

# Основной образ
FROM alpine:latest
RUN apk add --no-cache ca-certificates wget
COPY --from=chrome-downloader /opt/chrome-headless-shell-linux64 /opt/chrome/
ENV CHROME_BIN=/opt/chrome/chrome-headless-shell
# ... остальное
```

**Размеры образов:**
- С полным Chromium: ~300MB
- С chrome-headless-shell: ~200MB
- Без браузера (только HTTP scrape): ~20MB

### Продакшен рекомендации

1. **Используйте Alpine Linux** - минимальный базовый образ
2. **Multi-stage build** - уменьшает размер финального образа
3. **Non-root user** - безопасность
4. **Health checks** - мониторинг состояния
5. **Resource limits** - защита от OOM
6. **Volumes для cache** - персистентность данных

## Интеграция с llama.cpp

### Настройка в WebUI

1. Откройте llama.cpp WebUI
2. Перейдите в настройки MCP
3. Добавьте новый сервер:
   - **URL**: `http://127.0.0.1:8080/mcp`
   - **Включить прокси**: ✅ (для CORS)
   - **Заголовки**: (опционально) API ключ

### Использование в промптах

После подключения, инструменты доступны в AI промптах:

```
Пожалуйста, найди информацию о Golang и скрапи результаты с ключевых сайтов.
```

AI автоматически будет использовать инструменты `search_web` и `scrape_url`.

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
        tools.NewScrapeTool(),
        tools.NewScrapeJSTool(),  // ← JavaScript
        tools.NewSearchTool(),
        tools.NewParseHTMLTool(),
        tools.NewMyTool(),         // ← Новый инструмент
    }
    // ...
}
```

### Тестирование

```bash
# Запуск сервера
./mcp-web-scrape

# Тестирование health endpoint
curl http://localhost:8080/health

# Тестирование MCP tools/list
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/list"
  }' | jq

# Тестирование scrape_with_js
curl -X POST http://localhost:8080/mcp \
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

## Решённые проблемы

### 1. Файл с подчёркиванием не компилируется

**Проблема:** Файл `scraper_js.go` не включался в компиляцию пакета.

**Решение:** Переименовал в `js_tool.go`. В Go определённые паттерны именования файлов могут вызывать проблемы.

**Урок:** Избегайте подчёркиваний в названиях файлов, если они соответствуют специальным паттернам Go.

### 2. Бесконечная рекурсия в инструментах

**Проблема:** Первоначальная реализация `NewScrapeJSTool()` создавала рекурсию.

**Решение:** Изменил архитектуру - метод `Execute()` теперь часть структуры, а не замыкание.

**Урок:** Внимательно проектируйте жизненный цикл объектов в Go.

### 3. SSE библиотека не совместима

**Проблема:** `github.com/r3labs/sse/v2` имел проблемы с версионированием.

**Решение:** Реализовал собственный SSE transport, используя только стандартную библиотеку.

**Урок:** Для простых протоколов лучше использовать встроенные решения.

## Производительность и оптимизация

### Rate Limiting

- **Token Bucket алгоритм** - 10 запросов/сек, burst 20
- Настраивается через `rate_limit.requests_per_second`
- Предотвращает перегрузку сервера

### Кеширование

- **In-memory кеш** с TTL 5 минут
- Автоматическая очистка каждые 10 минут
- Настраивается через `cache.enabled`

### Оптимизации браузера

- **Блокировка изображений** - ускоряет загрузку на 50-70%
- **Оптимизация viewport** - уменьшает расход памяти
- **Reusable contexts** - переиспользование процессов Chrome

## Безопасность

### Рекомендации

1. **API ключи** - используйте переменные окружения для API ключей
2. **Rate limiting** - оставьте включенным для продакшена
3. **CORS** - ограничьте источники в продакшене
4. **Sandboxing** - используйте Docker изоляцию
5. **Logging** - настройте логирование уровня WARN для продакшена

### Аутентификация (будущая функциональность)

```yaml
mcp:
  api_key_header: X-API-Key
  api_key: ${MCP_API_KEY}  # Из переменной окружения
```

## Будущие улучшения

### Краткосрочные

- [ ] Redis кеширование
- [ ] Аутентификация/JWT
- [ ] Proxy rotation
- [ ] Sitemap parsing
- [ ] RSS feed parsing

### Среднесрочные

- [ ] WebSocket transport для MCP
- [ ] Асинхронные задачи
- [ ] Rate limiting per client
- [ ] Metrics & monitoring (Prometheus)
- [ ] Dashboard для мониторинга

### Долгосрочные

- [ ] Распределённый скрейпинг
- [ ] Machine Learning для контента
- [ ] Автоматическое обнаружение API
- [ ] Integration с другими MCP серверами

## API Endpoints

### Основные

- `GET /` - информация о сервере и инструментах
- `GET /health` - health check
- `GET /metrics` - метрики (rate limit, cache)
- `ANY /mcp` - MCP endpoint (HTTP POST + SSE)

### Примеры запросов

```bash
# Информация о сервере
curl http://localhost:8080/ | jq

# Health check
curl http://localhost:8080/health | jq

# Метрики
curl http://localhost:8080/metrics | jq

# MCP tools/list
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq

# MCP scrape_url
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "scrape_url",
      "arguments": {"url": "https://example.com"}
    }
  }' | jq

# MCP scrape_with_js
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "scrape_with_js",
      "arguments": {
        "url": "https://example.com",
        "screenshot": true
      }
    }
  }' | jq
```

## Мониторинг и логирование

### Уровни логирования

- **DEBUG** - детальная информация для отладки
- **INFO** - информационные сообщения (по умолчанию)
- **WARN** - предупреждения
- **ERROR** - ошибки

### Структура логов

```
[TIME] [LEVEL] [MESSAGE] key=value key2=value2
```

Пример:
```
19:42:02 INF Server listening addr=0.0.0.0:8080
19:42:02 INF Tool registered tool=scrape_with_js
19:42:09 INF HTTP request client_ip=::1 latency=0.228208 method=POST path=/mcp status=200
```

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
lsof -i :8080

# Использование другого порта
MCP_WEB_SCRAPE_SERVER_PORT=9090 ./mcp-web-scrape
```

### Проблемы с MCP

```bash
# Проверка MCP endpoint
curl -v http://localhost:8080/mcp

# Проверка инструментов
curl -X POST http://localhost:8080/mcp \
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

**Версия документа:** 1.0.0
**Последнее обновление:** 2026-04-30
**Статус:** Production Ready ✅
