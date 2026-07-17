# AGENTS.md — Инструкция для ИИ-ассистентов

## 1. Описание проекта

MCP Web Scrape Server — Go-сервер, реализующий протокол MCP (Model Context Protocol)
через JSON-RPC 2.0 поверх HTTP и SSE. Предназначен для веб-скрейпинга (статика через
HTTP, динамика через headless Chrome) и поиска. Интегрируется с llama.cpp WebUI,
Claude Desktop, Open WebUI (есть REST/OpenAPI-слой) и любым MCP-совместимым клиентом.

Self-hosted, single-instance, не multi-user (без аутентификации по умолчанию).
Запускается локально бинарником или в Docker (рекомендуется — Chrome предустановлен).

---

## 2. Технологический стек

**Язык:** Go 1.24 (`go.mod`).

**Бэкенд:**
- `gin-gonic/gin` v1.10 — HTTP-фреймворк, роутинг, middleware.
- `rs/zerolog` v1.33 — структурированное логирование.
- `spf13/viper` v1.18 — конфигурация (YAML + env с префиксом `MCP_WEB_SCRAPE_`).
- `golang.org/x/time/rate` — rate limiting (Token Bucket).

**Веб-скрейпинг:**
- `chromedp/chromedp` v0.11 + `chromedp/cdproto` — Chrome DevTools Protocol, headless Chrome для SPA.
- `PuerkitoBio/goquery` v1.9 — jQuery-like HTML-парсинг.
- `JohannesKaufmann/html-to-markdown` v1.6 — конвертация HTML→Markdown для LLM.
- `refraction-networking/utls` v1.8.2 — TLS-fingerprinting (Chrome 120 ClientHello), обход JA3/JA4-детекции.
- `patrickmn/go-cache` v2 — in-memory кеш с TTL по content-type.

**База данных / очередь:** не используются. Всё in-memory (кеш, rate limiter, browser pool).
Site-method learning (опционально) пишет в `./data/site_methods.yaml`.

**Внешние сервисы (не в репозитории):**
- Headless Chrome/Chromium — должен быть установлен локально или в Docker (в Dockerfile — `apk add chromium`).
- RAG-сервис (`rag.0x27.ru`) — опционально, включается флагом `rag.enabled`. Не часть этого репо.
- Поисковые провайдеры: DuckDuckGo (бесплатно, по умолчанию), Brave/Bing (требуют API-ключ).
- GitHub API — опциональный токен `github.token` (увеличивает лимит 60→5000 req/hour).

**Фронтенд:** отсутствует. Только JSON API и SSE.

**Тестирование:** стандартный `testing` + `go test`. Часть тестов — integration (делают
реальные HTTP-запросы к httpbin.org, wowhead.com — см. раздел 5).

**Линтер:** `go vet` + `golangci-lint` (цели в Makefile). Отдельного конфига `.golangci.yml` нет — дефолтные правила.

**Деплой:** Docker, multi-stage build (`Dockerfile`). Базовый runtime-образ `alpine:latest`
с предустановленным Chromium. `docker-compose.yml` задаёт лимиты (4 CPU / 4GB RAM,
shm 256MB — критично для Chrome), security_opt, ulimits, healthcheck.
Порт контейнера и хоста — **8192** (НЕ 8080, как местами в старом Makefile).

---

## 3. Архитектура и структура

```
mcp-web-scrape/
├── cmd/server/
│   └── main.go                 # Точка входа: init logger/cache/browser pool, Gin-роутер, graceful shutdown
├── internal/
│   ├── mcp/
│   │   ├── server.go           # MCP-сервер: регистрация tools, JSON-RPC dispatch (initialize/tools.list/tools.call/ping)
│   │   ├── transport.go        # HTTP/SSE транспорт, сессии, cleanup goroutine
│   │   ├── types.go            # MCP-типы (JSONRPCMessage, Tool, ServerInfo, ...)
│   │   └── tools/
│   │       ├── tool.go                  # Базовый интерфейс Tool + BaseTool (Name/Description/InputSchema/Execute)
│   │       ├── js_tool.go               # scrape_with_js — Chrome-скрейпер (динамика, GitHub, SPA, stealth, actions)
│   │       ├── scraper.go               # scrape_url — быстрый HTTP-скрейпер (статика)
│   │       ├── diagnostic_tool.go       # diagnostic_url — диагностика блокировок, выбор http/chrome
│   │       ├── search.go                # search_web — DuckDuckGo/Brave/Bing
│   │       ├── parser.go                # parse_html — CSS-селекторы
│   │       ├── smart_extractor.go       # smart_extract — режимы news/tech/finance/legal/medical/clean_text/links/catalog; clean_text и general через readability + regex-fallback
│   │       ├── rag_tool.go              # rag_* — 4 инструмента (только при rag.enabled=true)
│   │       ├── unified_scraper.go       # UnifiedScraper — авто-выбор HTTP vs Chrome по домену/js_sites
│   │       ├── chrome_scraper.go        # Низкоуровневый Chrome-скрейпер (chromedp)
│   │       ├── http_scraper.go          # Низкоуровневый HTTP-скрейпер (uTLS)
│   │       ├── scraper_interface.go     # Интерфейс Scraper
│   │       ├── html_optimizer.go        # Оптимизация HTML (удаление script/style/nav, -70..95%)
│   │       ├── catalog_discovery.go     # E-commerce: авто-обнаружение sitemap/catalog
│   │       ├── sitemap_parser.go        # Парсинг sitemap.xml
│   │       ├── retry.go                 # Retry-loop (Phase 5): повтор с ротацией прокси
│   │       ├── common.go                # Общие хелперы
│   │       └── *_test.go                # Тесты (часть — integration, с сетью)
│   └── pkg/                             # Внутренние пакеты (не экспортируются наружу)
│       ├── browser/                     # Pool headless Chrome, stealth (Phases 3), block_detector
│       ├── cache/                       # Кеш с TTL по content-type
│       ├── config/                      # Load() через viper, валидация
│       ├── converter/                   # HTML→Markdown
│       ├── domain/method_learner.go     # Site-method learning (опционально)
│       ├── http/tls_client.go           # uTLS-клиент (Chrome 120 fingerprint)
│       ├── logger/                      # zerolog init
│       ├── openapi/                     # REST/OpenAPI-слой для совместимости с Open WebUI
│       ├── proxy/rotator.go             # Ротация прокси
│       └── useragent/rotator.go         # Ротация User-Agent
├── config.yaml                 # Дефолтный конфиг (монтируется в Docker read-only)
├── config.yaml.example         # Пример конфигурации
├── docker-compose.yml          # Продакшен-конфиг: лимиты, security, healthcheck
├── Dockerfile                  # Multi-stage: golang:1.24-alpine → alpine + chromium
├── Makefile                    # build/run/test/docker-* цели
├── docs/
│   ├── GITHUB_TOKEN.md
│   ├── SITE_METHOD_LEARNING.md
│   └── archive/                # Архивные доки (RAG_INTEGRATION_ATTEMPTS, ROADMAP и т.д.)
├── examples/                   # Go-пример клиента + interactive/ JSON-сценарии
└── AGENTS.md                   # Этот файл
```

### Поток данных (типичный scrape_with_js)

```
Client (MCP JSON-RPC)
  → Gin router (/mcp или /sse)
    → mcp.Server.HandleMessage
      → rate limiter check
      → tools[name].Execute
        → UnifiedScraper (выбор по домену: js_sites → Chrome, иначе HTTP first)
          ├── ChromeScraper (chromedp): stealth-инъекции → навигация → actions → HTML
          └── HTTPScraper (uTLS): запрос с Chrome-fingerprint → HTML
        → html_optimizer (strip script/style/nav, -70..95%)
        → опц. converter (HTML→Markdown)
        → опц. RAG auto-index (если rag.enabled)
        → кеш (TTL по content-type)
      ← MCP content[] (text + опц. image base64) + _metadata
```

### Конкурентность

- **Browser pool** (`internal/pkg/browser`) — переиспользование Chrome-контекстов, `max_tabs` (default 10). Каждый scrape получает свой контекст, освобождается после.
- **Retry-loop** (Phase 5) — при блокировке создаёт новый browser context с другим прокси (до `browser.max_retries`, default 2).
- **Sessions** в transport.go — map под `sync.RWMutex`, фоновая goroutine cleanup.
- **Cache** (`go-cache`) — thread-safe.
- HTTP-сервер Gin — стандартный net/http goroutine-per-request.

### Роутинг (Gin)

Порядок регистрации в `cmd/server/main.go`:
- `GET /health`, `HEAD /health` — healthcheck (используется Docker healthcheck).
- `Any /mcp` (значение `mcp.endpoint` из конфига) — JSON-RPC endpoint.
- `Any /sse` — SSE для llama.cpp WebUI (тот же handler).
- OpenAPI routes — регистрируются `openapiHandler.RegisterRoutes(router)` (для Open WebUI).
- `GET /` — info: имя, версия, список endpoints, capabilities, tools.
- `GET /metrics` — rate_limit/cache/browser_pool/user_agent/proxy статистика.

---

## 4. Конвенции и кодстайл

**Go:**
- Стандартный `gofmt` / `goimports`. Перед коммитом: `make format` (= `go fmt ./... && gofmt -w .`).
- Импорты группами через пустую строку: stdlib → сторонние → локальные (`github.com/metall/mcp-web-scrape/...`). См. любой файл в `internal/`.
- Package names — короткие, строчные, без `_` и `camelCase`: `mcp`, `tools`, `browser`, `cache`.
- Экспортируемые идентификаторы — PascalCase, внутренние — camelCase. Никаких венгерских префиксов.
- Interface-имена: существительное (`Scraper`), не `IScraper`.
- Ошибки оборачиваются через `fmt.Errorf("...: %w", err)`. На границах (MCP handler) логируются через zerolog и возвращаются как JSON-RPC error.
- Структура конфига — отдельный `*Config` struct на каждую доменную область в `internal/pkg/config/config.go` (`ServerConfig`, `BrowserConfig`, `ScrapingConfig`, ...). Главная — `Config`.

**MCP-инструменты:**
- Каждый инструмент — отдельный файл в `internal/mcp/tools/`, struct с embedded `*BaseTool`.
- Конструктор `NewXxxTool(...)` регистрирует имя, описание, JSON-schema и handler. Имя — строковой литерал в `NewBaseTool("name", ...)`.
- Регистрация — в `mcp.Server.registerDefaultTools()` (`internal/mcp/server.go`). Порядок важен (`toolsOrder` сохраняется для детерминированного `tools/list`).
- Условная регистрация: RAG-инструменты добавляются только при `config.RAG.Enabled`.

**Обработка ошибок:**
- В скрейперах используется `ScrapeError` с классификацией (timeout / blocked / captcha / partial). Retry-loop в `retry.go` реагирует на класс.
- HTTP-fallback: при падении Chrome scraper автоматически ретраится через HTTPScraper.
- Panic-recovery — на уровне Gin middleware (`gin.Recovery()`).

**Безопасность:**
- Сервер **не имеет аутентификации** по умолчанию — рассчитан на доверенную сеть / localhost. Не выставлять в публичный интернет без reverse-proxy с auth.
- `github.token` и `search.api_key` — в config.yaml или env (`GITHUB_TOKEN`), **никогда** не коммитятся (config.yaml в .gitignore, есть config.yaml.example).
- Docker: `no-new-privileges`, read-only mount config, unprivileged user `mcp`.

**Конфигурация:**
- Единый источник правды — `internal/pkg/config/config.go` (struct- definitions) + `setDefaults()` в нём же.
- Новая настройка → добавить поле в нужный `*Config` struct + default в `setDefaults` + при необходимости ключ в `config.yaml.example`. Viper автоматически мапит env `MCP_WEB_SCRAPE_<SECTION>_<KEY>`.

---

## 5. Тесты и проверки

### Главное правило: тесты в Docker

Тесты запускаются **внутри Docker-контейнера**, не локально на маке. Это даёт:
- идентичное окружение с предустановленным Chromium (часть тестов гоняет chromedp);
- изоляцию интеграционных тестов, которые ходят на реальные сайты (httpbin.org, wowhead.com);
- воспроизводимость результата на любой машине.

Для этого в `Dockerfile` есть отдельная `test`-стадия: `golang:1.24-alpine` + Chromium.
В `.dockerignore` `*_test.go` НЕ исключаются (исключены только собранные `*.test`-бинари).

### Запуск тестов в Docker

```bash
# Полный прогон (unit + integration, ходит в сеть, нужен Chrome)
make docker-test
# = docker build --target test -t mcp-web-scrape:test . && docker run --rm mcp-web-scrape:test

# Только быстрые unit-тесты (без сети, ~2 сек)
make docker-test-unit
# = docker run --rm mcp-web-scrape:test go test ./internal/pkg/cache/... ./internal/pkg/config/...

# Произвольная команда в test-образе (например, один тест, vet)
docker run --rm mcp-web-scrape:test go test -run TestNewPool ./internal/pkg/browser/
docker run --rm mcp-web-scrape:test go vet ./...
```

### Линт и форматирование

```bash
# Линт (можно локально, go vet не требует Chrome)
make vet              # go vet ./...

# Форматирование — ВНИМАНИЕ: `make format` переформатирует ВЕСЬ репо
# (gofmt выравнивает struct-tags), что создаёт drive-by шум в diff.
# Перед коммитом запускают только на изменённых файлах:
gofmt -w cmd/server/main.go internal/pkg/config/config.go
```

Примечание: часть файлов репо исторически не-gofmt (struct-tag alignment).
Не «чинить» это драйв-баем — затрагивает только то, что нужно задаче.

### Локальный запуск (исключение, не по умолчанию)

Локально на маке разрешена только сборка/запуск (`make build`, `./mcp-web-scrape`),
которые не требуют Chrome и не оставляют артефактов. Полноценное тестирование — через Docker.

```bash
make build            # → ./mcp-web-scrape (локальная сборка OK)
make run              # запуск сервера
```

### Сборка и поднятие приложения

```bash
docker compose up -d --build
curl http://localhost:8192/health
```

### Известные проблемы тестов

- Часть тестов в `internal/mcp/tools/` — **integration**: реальные HTTP-запросы к
  httpbin.org, wowhead.com. Без сети падают. Для CI/оффлайн — `docker-test-unit`.
- `internal/pkg/browser/browser_test.go` ранее импортировал `testcontainers-go`
  (нет в go.mod) и ломал `go vet`. Переписан на тесты пула без внешних зависимостей.

---

## 6. Git workflow и багтрекинг

### Ветвление

```
master              — стабильный, только через PR
feat/N-description  — новые фичи
fix/N-description   — багфиксы
```

`N` — номер issue в Gitea. Дефис-разделители, snake_case не используем.

### Процесс

1. Issue в Gitea (`https://gitea.0x27.ru/pub/metall_mcp-web-scrape/issues`) с описанием задачи/бага.
2. `git pull` (обновить master перед созданием ветки).
3. `git checkout -b feat/NN-description` (NN = номер issue).
4. Работа, коммиты. **В PR description (обязательно)** и/или в заголовке последнего коммита — ключевое слово для авто-закрытия issue. Принимаются **только**:
   - `Fixes #NN`
   - `Closes #NN`
   - `Resolves #NN`

   ❌ Не работают: «issue #NN», «#NN», «fix for NN», «задача NN».
5. Push ветки → PR в Gitea (base: master). В **PR description** добавить `Fixes #NN` (Gitea закрывает issue при мерже PR, даже если в commit message не было).
6. Review → merge PR **в Gitea** (через UI или API `POST /repos/pub/metall_mcp-web-scrape/pulls/N/merge`).
7. PR-merge автоматически закрывает issue по ссылке `Fixes #N`.
8. Синхронизировать локальный master: `git checkout master && git pull origin master`.
9. Удалить ветки (и локальную, и удалённую):
   ```
   git branch -d feat/NN-description
   git push origin --delete feat/NN-description
   ```
   Gitea также удаляет source branch автоматически при merge, если включён соответствующий лимит.

**Важно:** работа не завершена, пока изменения не влиты в master на Gitea и локальный master не синхронизирован. Локальный коммит без push/merge = задача не выполнена. Не оставляйте результаты только в локальной ветке. Не оставляйте слитые ветки — мусор в списке усложняет навигацию.

### Ремоуты

- `origin` → `https://gitea.0x27.ru/pub/metall_mcp-web-scrape.git` (основной, сюда делаются PR).
- `github` → `git@github.com:metall27/mcp-web-scrape.git` (зеркало).

**Внимание:** module path в `go.mod` — `github.com/metall/mcp-web-scrape`, а реальный
репозиторий — `metall27/...`. Внутренние импорты резолвятся локально, но
`go install github.com/metall/mcp-web-scrape/cmd/server@...` из интернета будет 404.
При переименовании нужно синхронно править все `import`-пути.

### Доступ к Gitea API

Базовый URL: `https://gitea.0x27.ru/api/v1`. Можно создавать/закрывать issues, читать PR,
навешивать labels, постить ревью через curl. Авторизация — basic-auth (доступна из
окружения, не хардкодить креды в файлы).

---

## 7. Roadmap

Roadmap ведётся в Gitea через **Milestones** и **Issues**:
`https://gitea.0x27.ru/pub/metall_mcp-web-scrape/milestones`.

Отдельный `todo.md` / `ROADMAP_NEXT_SESSION.md` не ведётся — единый источник правды в Gitea.
Архивные планировочные документы лежат в `docs/archive/` (исторические, не активные).
