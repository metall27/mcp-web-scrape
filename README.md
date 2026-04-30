# MCP Web Scrape Server

MCP (Model Context Protocol) server for web scraping and search capabilities, designed to work with llama.cpp.

## Features

- **Web Scraping**: Fetch and extract content from URLs
- **JavaScript Rendering**: Headless Chrome support for dynamic content via `scrape_with_js`
- **Search Integration**: Web search via DuckDuckGo (free), Brave Search API, or Bing Search API
- **HTML Parsing**: Extract elements using CSS selectors
- **MCP Protocol**: Full support for MCP over HTTP/SSE transport
- **Rate Limiting**: Built-in protection against abuse
- **Caching**: In-memory caching for improved performance
- **Docker Support**: Production-ready Docker images with Chrome pre-configured

## Installation

### Prerequisites

For basic HTTP scraping:
- Go 1.21 or higher

For JavaScript rendering (`scrape_with_js` tool):
- Chrome/Chromium browser (see below for installation options)
- **OR** Docker (recommended - Chrome included in image)

Optional:
- Brave Search API key for Brave search
- Bing Search API key for Bing search

### Chrome/Chromium Installation

#### Option 1: Docker (Recommended ✅)

Docker includes headless Chrome automatically - no manual installation needed:

```bash
docker-compose up -d
```

#### Option 2: System-wide Chromium (Linux servers)

**Ubuntu/Debian:**
```bash
# Install Chromium WITHOUT GUI dependencies
sudo apt-get update
sudo apt-get install -y chromium-browser --no-install-recommends
```

**Alpine Linux:**
```bash
# Chromium in Alpine = NO GUI dependencies
apk add --no-cache chromium
```

**CentOS/RHEL:**
```bash
sudo yum install -y chromium
```

#### Option 3: Chrome Headless Shell (Minimal)

Special headless-only version from Google:

```bash
wget https://storage.googleapis.com/chrome-for-testing/public/123.0.6312.58/linux64/chrome-headless-shell-linux64.zip
unzip chrome-headless-shell-linux64.zip
sudo mv chrome-headless-shell-linux64/chrome-headless-shell /usr/local/bin/
```

> **⚠️ Important:** For server environments, use `--no-install-recommends` (Debian/Ubuntu) or Docker to avoid installing GUI dependencies.

### Build (from source)

```bash
# Clone repository
git clone https://github.com/metall/mcp-web-scrape.git
cd mcp-web-scrape

# Install dependencies
go mod download

# Build
go build -o mcp-web-scrape ./cmd/server
```

### Run

```bash
# Run with default config
./mcp-web-scrape

# Run with custom config
./mcp-web-scrape -config config.yaml

# Set port via environment variable
MCP_WEB_SCRAPE_SERVER_PORT=9090 ./mcp-web-scrape
```

### Docker (Recommended for production)

Docker is the easiest way to run the server with JavaScript rendering support.

```bash
# Build and run with docker-compose
docker-compose up -d

# Or build manually
docker build -t mcp-web-scrape:latest .
docker run -d -p 8080:8080 --name mcp-server mcp-web-scrape:latest

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

**Docker ports:**
- `8080` - HTTP/MCP server endpoint

**Health check:**
```bash
# Check container health
docker ps

# Test endpoint
curl http://localhost:8080/health
```

**Docker features:**
- ✅ Headless Chrome pre-installed (no GUI dependencies)
- ✅ Multi-stage build for smaller image size
- ✅ Non-root user for security
- ✅ Health checks for monitoring
- ✅ Resource limits configured
- ✅ Volume mounts for configuration and cache

## Configuration

Configuration can be provided via YAML file or environment variables:

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_WEB_SCRAPE_SERVER_HOST` | Server host | `0.0.0.0` |
| `MCP_WEB_SCRAPE_SERVER_PORT` | Server port | `8080` |
| `MCP_WEB_SCRAPE_MCP_ENDPOINT` | MCP endpoint | `/mcp` |
| `MCP_WEB_SCRAPE_LOG_LEVEL` | Log level | `info` |

### Configuration File (config.yaml)

See `config.yaml` for all available options.

## MCP Tools

### scrape_url

Scrapes content from a URL.

**Parameters:**
- `url` (string, required): URL to scrape
- `timeout` (integer, optional): Timeout in seconds (default: 30)
- `user_agent` (string, optional): Custom user agent
- `headers` (object, optional): Custom HTTP headers

**Example:**
```json
{
  "name": "scrape_url",
  "arguments": {
    "url": "https://example.com"
  }
}
```

### scrape_with_js

Scrapes content with JavaScript rendering using headless Chrome.

**Parameters:**
- `url` (string, required): URL to scrape
- `timeout` (integer, optional): Page load timeout in seconds (default: 30)
- `wait_for` (string, optional): CSS selector to wait for before scraping
- `wait_time` (integer, optional): Additional wait time in milliseconds (default: 1000)
- `screenshot` (boolean, optional): Take screenshot of page (default: false)
- `user_agent` (string, optional): Custom user agent string
- `viewport_width` (integer, optional): Browser viewport width in pixels (default: 1920)
- `viewport_height` (integer, optional): Browser viewport height in pixels (default: 1080)
- `block_images` (boolean, optional): Block images from loading (faster, default: false)

**Example:**
```json
{
  "name": "scrape_with_js",
  "arguments": {
    "url": "https://react.example.com",
    "wait_for": ".app-loaded",
    "screenshot": true,
    "block_images": true
  }
}
```

### search_web

Searches the web.

**Parameters:**
- `query` (string, required): Search query
- `max_results` (integer, optional): Max results (default: 10)
- `provider` (string, optional): `duckduckgo`, `brave`, `bing` (default: `duckduckgo`)
- `safe_search` (boolean, optional): Enable safe search (default: true)

**Example:**
```json
{
  "name": "search_web",
  "arguments": {
    "query": "golang web scraping",
    "max_results": 5
  }
}
```

### parse_html

Parses HTML and extracts elements.

**Parameters:**
- `html` (string, required): HTML content
- `selector` (string, optional): CSS selector (default: `*`)
- `extract` (string, optional): `text`, `html`, `attr`, `all` (default: `text`)
- `attribute` (string, optional): Attribute name (required when extract=`attr`)
- `metadata` (boolean, optional): Include metadata (default: false)

**Example:**
```json
{
  "name": "parse_html",
  "arguments": {
    "html": "<html>...</html>",
    "selector": "a.link",
    "extract": "attr",
    "attribute": "href"
  }
}
```

## Integration with llama.cpp

### Add MCP Server to llama.cpp WebUI

#### Option 1: Docker (Recommended)

1. Start the MCP server:
```bash
docker-compose up -d
```

2. In llama.cpp WebUI, go to MCP settings

3. Add MCP server:
   - **URL**: `http://host.docker.internal:8080/mcp` (Docker Desktop)
   - **OR**: `http://172.17.0.1:8080/mcp` (Linux Docker)
   - **OR**: `http://localhost:8080/mcp` (if port forwarded)
   - **Enable proxy**: ✅ Check this for CORS support
   - **Headers**: (optional) Add API key if configured

#### Option 2: Local binary

1. Start the MCP server:
```bash
./mcp-web-scrape
```

2. In llama.cpp WebUI, go to MCP settings

3. Add MCP server:
   - **URL**: `http://127.0.0.1:8080/mcp`
   - **Enable proxy**: ✅ Check this for CORS support
   - **Headers**: (optional) Add API key if configured

4. Test connection and tools will be available

**Available tools after connection:**
- `scrape_url` - HTTP scraping
- `scrape_with_js` - JavaScript rendering (requires Chrome)
- `search_web` - Web search
- `parse_html` - HTML parsing with CSS selectors

## API Endpoints

- `GET /` - Server info
- `GET /health` - Health check
- `ANY /mcp` - MCP endpoint (supports HTTP POST and SSE)
- `GET /metrics` - Server metrics

## Development

### Project Structure

```
mcp-web-scrape/
├── cmd/
│   └── server/          # Main server application
├── internal/
│   ├── mcp/             # MCP protocol implementation
│   │   └── tools/       # MCP tools
│   └── pkg/             # Internal packages
│       ├── cache/       # Caching layer
│       ├── config/      # Configuration
│       └── logger/      # Logging
├── config.yaml          # Example configuration
└── README.md
```

### Adding New Tools

Create a new tool in `internal/mcp/tools/`:

```go
package tools

type MyTool struct {
    *BaseTool
}

func NewMyTool() *MyTool {
    // ... implement tool interface
}
```

Register in `internal/mcp/server.go`:

```go
func (s *Server) registerDefaultTools() error {
    // ...
    s.RegisterTool(tools.NewMyTool())
    return nil
}
```

## Future Enhancements

- [ ] JavaScript rendering (chromedp/playwright)
- [ ] Headless browser support
- [ ] Redis caching support
- [ ] Authentication/Authorization
- [ ] More search providers
- [ ] Proxy rotation
- [ ] Sitemap parsing
- [ ] RSS feed parsing
- [ ] Output formats (Markdown, plain text, etc.)

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
