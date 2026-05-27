# RAG Integration Attempts - Technical Summary

## Problem Statement

**Goal:** Force LLM (llama.cpp + Gemma 4 31B) to use `rag_search` instead of `scrape_with_js` for information retrieval requests.

**User Request:** "посмотри документацию на https://github.com/metall27/mp-web-scrape, что нужно для установки?"

**LLM Behavior:** Always chooses `scrape_with_js`, never `rag_search`.

## Attempted Solutions (Chronological)

### 1. Tool Description Changes

**Approach:** Modify tool descriptions to guide LLM behavior.

**Attempts:**
1. Explicit warnings: "DO NOT USE unless user says 'scrape'/'fetch'/'download'"
2. Question patterns: "For 'tell me about X', 'how to use X', 'documentation for X'"
3. "ALWAYS use FIRST" directive with emoji (⚠️, 🛑, 🚨)
4. Content type logic: Dynamic vs stable content distinction

**Result:** ❌ LLM ignored descriptions, kept using `scrape_with_js`

### 2. Tool Registration Order

**Problem:** Go `map` iteration order is random, LLM saw tools in different order each request.

**Solution:** Added `toolsOrder []string` slice to preserve registration order.

**Code Changes:**
```go
type Server struct {
    tools       map[string]tools.Tool
    toolsOrder   []string // NEW: Preserve registration order
}

func (s *Server) RegisterTool(tool tools.Tool) error {
    name := tool.Name()
    s.tools[name] = tool
    s.toolsOrder = append(s.toolsOrder, name) // NEW
    return nil
}

func (s *Server) handleToolsList(ctx context.Context, req *JSONRPCMessage) ([]byte, error) {
    // Iterate toolsOrder instead of map
    for _, toolName := range s.toolsOrder {
        tool := s.tools[toolName]
        // ...
    }
}
```

**Commits:**
- `50673f1` Preserve tool registration order for tools/list

**Result:** ✅ `rag_search` now consistently first in tools/list

### 3. Hardcoded URLs in Descriptions

**Approach:** Explicitly list indexed URLs in `rag_search` description.

**Attempts:**
1. "Already indexed URLs include: https://github.com/metall27/mcp-web-scrape, https://kubernetes.io"
2. "ALREADY INDEXED: https://github.com/metall27/mcp-web-scrape (128 chunks). DO NOT scrape"

**Result:** ❌ LLM still used `search_web` or `scrape_with_js`

**User Feedback:** "Зачем ты меня заставляешь искать енвы, перед тобой источник истины: репозиторий с исходным кодом"

### 4. Final Solution: Auto-Indexing

**Approach:** Stop fighting LLM behavior. Make `scrape_with_js` automatically index to RAG.

**Architecture:**
```
LLM Request → scrape_with_js
             ↓
           1. Scrape URL (Chrome)
           2. Auto-index to RAG (goroutine, non-blocking)
           3. Return HTML immediately
             ↓
           Next request → RAG has content
```

**Code Implementation:**

**File:** `internal/mcp/tools/js_tool.go`

**Imports Added:**
```go
import (
    "bytes"
    "encoding/json"
    "os"
)
```

**Auto-Indexing Logic:**
```go
// After successful scraping, before return
go func() {
    ragBaseURL := "https://rag.0x27.ru"
    if envBaseURL := os.Getenv("RAG_BASE_URL"); envBaseURL != "" {
        ragBaseURL = envBaseURL
    }

    indexReq := map[string]interface{}{
        "url": urlStr,
        "processing_mode": "structured",
        "ttl": 7,
    }

    jsonData, _ := json.Marshal(indexReq)

    resp, err := http.Post(
        ragBaseURL+"/api/v1/index",
        "application/json",
        bytes.NewBuffer(jsonData),
    )
    // Log success/failure, don't block response
}()
```

**Commits:**
- `b451681` Add auto-indexing to scrape_with_js
- `a4b3c7f` Clean up tool descriptions - remove hardcoded URLs

## Current State

**Tool Descriptions (Final):**
- `scrape_with_js`: "Get HTML content from URLs... Auto-indexes to RAG for future semantic search"
- `rag_search`: "Search indexed documentation... scrape_with_js auto-indexes to RAG"

**Tool Order:**
1. rag_search
2. rag_index
3. rag_health
4. rag_list_documents
5. scrape_with_js

**Servers:**
- **skynet.0x27.ru**: nginx proxy → mcp-web-scrape (port 8192)
- **rag.0x27.ru**: RAG service (kali home server, RTX 3060)
- **llama.cpp**: Gemma 4 31B on skynet (systemd: llama.service)

## Technical Details

### RAG Service Status

**Health:** `{"status":"degraded","components":{"embeddings":"healthy","vector_store":"healthy","scraper":"unknown"}}`

**Indexed Documents:**
- https://github.com/metall27/mcp-web-scrape: 128 chunks, 190KB
- https://kubernetes.io: various docs
- Others

**Search Behavior:**
- Russian "Установка": ✅ Finds results
- English "installation": ❌ No results (multilingual embedding limitation)

### MCP Endpoints

**skynet Configuration:**
```
location /mcp → proxy_pass http://172.31.0.22:8192/mcp
location /sse → proxy_pass http://172.31.0.22:8192/sse
```

**Environment Variables:**
- `RAG_BASE_URL`: https://rag.0x27.ru (default, can override)

## Server Access Matrix

| Server | SSH Access | Role |
|--------|-----------|------|
| kali | ✅ ssh kali | RAG service (rag.0x27.ru) |
| skynet | ❌ No SSH | mcp-web-scrape + nginx proxy |
| llama.cpp | systemd | On skynet |

## Important Reminders

**MEMORY RULE:** No SSH to skynet! Only access via:
1. HTTP/HTTPS requests
2. nginx proxy configuration
3. User manually deploys

**Deployment:**
```bash
# On skynet (manually by user)
cd /root/mcp-web-scrape
git pull
docker compose restart

# On kali (ssh kali)
cd /home/kali/rag-research-agent
git pull
ps aux | grep uvicorn | grep -v grep | awk '{print $2}' | xargs kill
nohup venv/bin/uvicorn app.main:app --host 0.0.0.0 --port 8000 > logs/rag_agent.log 2>&1 &

# llama.cpp restart
sudo systemctl restart llama
```

## Next Steps

**To test auto-indexing:**
1. Update skynet with commit `a4b3c7f`
2. Restart services
3. Make request: "what tools are in mcp-web-scrape"
4. Check if indexed in RAG: `curl -s https://rag.0x27.ru/api/v1/documents`
5. Make same request again - should find in RAG

**Potential improvements:**
1. Check RAG cache before scraping, return indexed content if available
2. Add retry logic for RAG indexing failures
3. Add RAG health check before indexing
4. Multi-language support (currently Russian-only for some docs)

## Commit History

```
b451681 Add auto-indexing to scrape_with_js
a4b3c7f Clean up tool descriptions - remove hardcoded URLs
1ddbd0a Make rag_search description ultra-specific with chunk count
94ea450 Add explicit indexed URLs to rag_search description
3949c79 Add content type logic to tool descriptions
8e9e08d Add explicit question patterns to tool descriptions
3dac54b Make descriptions neutral and positive
50673f1 Preserve tool registration order for tools/list
2cb5f35 Make descriptions ultra-short and direct
```
