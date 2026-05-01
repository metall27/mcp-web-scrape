# Stage 1: Build
FROM golang:1.21-alpine AS builder

# Установка зависимостей для сборки
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Копирование go mod файлов
COPY go.mod go.sum ./
RUN go mod download

# Копирование исходного кода
COPY . .

# Сборка бинарника
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o mcp-web-scrape ./cmd/server

# Stage 2: Runtime
FROM alpine:latest

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
