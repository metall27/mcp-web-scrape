# MCP Web Scrape Server

MCP (Model Context Protocol) server for web scraping and search capabilities, designed to work with llama.cpp.

## Features

- **Web Scraping**: Fetch and extract content from URLs
- **Search Integration**: Web search via DuckDuckGo (free), Brave Search API, or Bing Search API
- **HTML Parsing**: Extract elements using CSS selectors
- **MCP Protocol**: Full support for MCP over HTTP/SSE transport
- **Rate Limiting**: Built-in protection against abuse
- **Caching**: In-memory caching for improved performance

## Installation

### Prerequisites

- Go 1.21 or higher
- (Optional) Brave Search API key for Brave search
- (Optional) Bing Search API key for Bing search

### Build

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

1. Start the MCP server:
```bash
./mcp-web-scrape
```

2. In llama.cpp WebUI, go to MCP settings

3. Add MCP server:
   - **URL**: `http://127.0.0.1:8080/mcp`
   - **Enable proxy**: Check this for CORS support
   - **Headers**: (optional) Add API key if configured

4. Test connection and tools will be available

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
