# Stage 1: Build
FROM golang:1.24-alpine AS builder

# Build metadata injected via -ldflags into internal/pkg/version (#63).
# GIT_SHA is taken from the build-arg (passed by Makefile/docker-compose as the
# current short SHA); BUILD_DATE is fixed at image-build time for reproducibility.
ARG VERSION=1.1.0
ARG GIT_SHA=unknown
ARG BUILD_DATE
ENV BUILD_DATE=${BUILD_DATE:-unknown}

# Nexus proxy переподписывает APKINDEX своим ключом (key-f14d99e5).
# golang:1.24-alpine → Alpine 3.23, поэтому пути к репозиториям — v3.23.
COPY public-apk.pem /etc/apk/keys/key-f14d99e5.rsa.pub

# Alpine репозитории через nexus proxy
# ВРЕМЕННО ЗАКОММЕНТИРОВАНО: nexus.0x27.ru недоступен (#63 PR). Используются
# дефолтные Alpine-репозитории. Раскомментировать, когда nexus снова онлайн.
#RUN echo "https://nexus.0x27.ru/repository/alpine-proxy/v3.23/main" > /etc/apk/repositories \
#    && echo "https://nexus.0x27.ru/repository/alpine-proxy/v3.23/community" >> /etc/apk/repositories

# Установка зависимостей для сборки
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Копирование go mod файлов
COPY go.mod go.sum ./
RUN go mod download

# Копирование исходного кода
COPY . .

# Сборка бинарника с инжекцией версии/коммита/даты (#63).
# GIT_SHA/BUILD_DATE приходят из build-args (см. ARG выше).
# -w -s вырезают DWARF/символы для меньшего образа.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags="-w -s \
      -X github.com/metall/mcp-web-scrape/internal/pkg/version.Version=${VERSION} \
      -X github.com/metall/mcp-web-scrape/internal/pkg/version.GitCommit=${GIT_SHA} \
      -X github.com/metall/mcp-web-scrape/internal/pkg/version.BuildDate=${BUILD_DATE}" \
    -o mcp-web-scrape ./cmd/server

# Stage 2: Test
# Образ для запуска тестов в Docker. Содержит Go-тулчейн + Chromium,
# чтобы integration-тесты (chrome_scraper, http_scraper) могли ходить
# к реальным сайтам в headless-режиме. Запуск:
#   docker build --target test -t mcp-web-scrape:test .
#   docker run --rm mcp-web-scrape:test
#   docker run --rm mcp-web-scrape:test go test -run TestNewPool ./internal/pkg/browser/
FROM golang:1.24-alpine AS test

# Nexus proxy переподписывает APKINDEX своим ключом (key-f14d99e5).
# golang:1.24-alpine → Alpine 3.23, поэтому пути к репозиториям — v3.23.
COPY public-apk.pem /etc/apk/keys/key-f14d99e5.rsa.pub

# Alpine репозитории через nexus proxy
# ВРЕМЕННО ЗАКОММЕНТИРОВАНО: nexus.0x27.ru недоступен (#63 PR). Используются
# дефолтные Alpine-репозитории. Раскомментировать, когда nexus снова онлайн.
#RUN echo "https://nexus.0x27.ru/repository/alpine-proxy/v3.23/main" > /etc/apk/repositories \
#    && echo "https://nexus.0x27.ru/repository/alpine-proxy/v3.23/community" >> /etc/apk/repositories

RUN apk add --no-cache git ca-certificates chromium

WORKDIR /app

COPY --from=builder /app/ /app/

# Хром в Alpine — headless, без X11/GTK. Нужен для chromedp.
ENV CHROME_BIN=/usr/bin/chromium-browser
ENV MCP_WEB_SCRAPE_BROWSER_NO_SANDBOX=true

# Дефолтная команда — полный прогон. Чтобы прогнать только unit (без сети),
# переопределить аргументом: docker run --rm mcp-web-scrape:test \
#   go test ./internal/pkg/cache/... ./internal/pkg/config/...
CMD ["go", "test", "./..."]

# Stage 3: Runtime
FROM alpine:3.23

# Nexus proxy переподписывает APKINDEX своим ключом (key-f14d99e5).
COPY public-apk.pem /etc/apk/keys/key-f14d99e5.rsa.pub

# Alpine репозитории через nexus proxy
# ВРЕМЕННО ЗАКОММЕНТИРОВАНО: nexus.0x27.ru недоступен (#63 PR). Используются
# дефолтные Alpine-репозитории. Раскомментировать, когда nexus снова онлайн.
#RUN echo "https://nexus.0x27.ru/repository/alpine-proxy/v3.23/main" > /etc/apk/repositories \
#    && echo "https://nexus.0x27.ru/repository/alpine-proxy/v3.23/community" >> /etc/apk/repositories

# Установка Chromium БЕЗ GUI зависимостей
# Chromium в Alpine = только headless, без X11/GTK
RUN apk add --no-cache \
    chromium \
    ca-certificates \
    wget \
    && rm -rf /var/cache/apk/* \
    /var/tmp/* \
    /tmp/*

# Создание пользователя без привилегий
RUN addgroup -g 1000 -S mcp && \
    adduser -u 1000 -S mcp -G mcp

WORKDIR /app

# Копирование бинарника из builder stage
COPY --from=builder /app/mcp-web-scrape /app/
COPY config.yaml /app/

# Настройка прав доступа
RUN chown -R mcp:mcp /app

# Переключение на пользователя mcp
USER mcp

# Порты
# 8192 - HTTP/MCP server
EXPOSE 8192

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8192/health || exit 1

# Переменные окружения по умолчанию
ENV MCP_WEB_SCRAPE_SERVER_HOST=0.0.0.0
ENV MCP_WEB_SCRAPE_SERVER_PORT=8192
ENV MCP_WEB_SCRAPE_LOG_LEVEL=info
ENV MCP_WEB_SCRAPE_BROWSER_ENABLED=true
ENV MCP_WEB_SCRAPE_BROWSER_HEADLESS=true
ENV MCP_WEB_SCRAPE_BROWSER_NO_SANDBOX=true

CMD ["./mcp-web-scrape"]
