package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

type Transport struct {
	server   *Server
	logger   zerolog.Logger
	sessions map[string]*Session
	mu       sync.RWMutex
}

type Session struct {
	ID        string
	CreatedAt time.Time
	LastSeen  time.Time
	Channel   chan []byte
	mu        sync.RWMutex
}

func NewTransport(server *Server) *Transport {
	t := &Transport{
		server:   server,
		logger:   logger.Get(),
		sessions: make(map[string]*Session),
	}

	// Start session cleanup goroutine
	go t.cleanupSessions()

	return t
}

// HandleMCP handles MCP protocol requests
func (t *Transport) HandleMCP(w http.ResponseWriter, r *http.Request) {
	t.logger.Info().
		Str("method", r.Method).
		Str("path", r.URL.Path).
		Msg("MCP request received")

	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		t.handleCORS(w, r)
		return
	}

	// Check for SSE upgrade
	if r.Header.Get("Accept") == "text/event-stream" {
		t.handleSSE(w, r)
		return
	}

	// Handle HTTP POST with JSON-RPC
	if r.Method == http.MethodPost {
		t.handleHTTPPost(w, r)
		return
	}

	// Handle GET for server info
	if r.Method == http.MethodGet {
		t.handleServerInfo(w, r)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (t *Transport) handleCORS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
	w.WriteHeader(http.StatusOK)
}

func (t *Transport) handleSSE(w http.ResponseWriter, r *http.Request) {
	t.logger.Info().Msg("Establishing SSE connection")

	// Create session
	sessionID := GenerateSessionID()
	session := &Session{
		ID:        sessionID,
		CreatedAt: time.Now(),
		LastSeen:  time.Now(),
		Channel:   make(chan []byte, 100),
	}

	// Store session
	t.mu.Lock()
	t.sessions[sessionID] = session
	t.mu.Unlock()

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Ensure we can flush
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send initial message
	t.sendSSEMessage(w, flusher, "connected", map[string]interface{}{
		"session_id": sessionID,
		"server_info": t.server.GetServerInfo(),
	})

	// Send server capabilities
	t.sendSSEMessage(w, flusher, "capabilities", map[string]interface{}{
		"capabilities": t.server.GetCapabilities(),
	})

	// Keep connection alive
	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Listen for client disconnect
	go func() {
		<-ctx.Done()
		t.logger.Info().
			Str("session_id", sessionID).
			Msg("SSE client disconnected")
	}()

	for {
		select {
		case <-ticker.C:
			// Send keepalive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()

		case msg, ok := <-session.Channel:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()

		case <-ctx.Done():
			// Cleanup session
			t.mu.Lock()
			delete(t.sessions, sessionID)
			t.mu.Unlock()
			close(session.Channel)
			return
		}
	}
}

func (t *Transport) handleHTTPPost(w http.ResponseWriter, r *http.Request) {
	// Validate content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && contentType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	// Read request body
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Process message
	response, err := t.server.HandleMessage(r.Context(), data)
	if err != nil {
		t.logger.Error().Err(err).Msg("Failed to handle MCP message")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Write(response)
}

func (t *Transport) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"name":        t.server.GetServerInfo().Name,
		"version":     t.server.GetServerInfo().Version,
		"protocol":    "MCP over HTTP/SSE",
		"capabilities": t.server.GetCapabilities(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(info)
}

func (t *Transport) sendSSEMessage(w io.Writer, flusher http.Flusher, event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()

	return nil
}

// GetSession returns a session by ID
func (t *Transport) GetSession(sessionID string) (*Session, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	session, exists := t.sessions[sessionID]
	return session, exists
}

// cleanupSessions periodically removes old sessions
func (t *Transport) cleanupSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		t.CleanupOldSessions(30 * time.Minute)
	}
}

// CleanupOldSessions removes sessions older than the specified duration
func (t *Transport) CleanupOldSessions(maxAge time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for id, session := range t.sessions {
		if now.Sub(session.LastSeen) > maxAge {
			close(session.Channel)
			delete(t.sessions, id)
			t.logger.Info().
				Str("session_id", id).
				Dur("age", now.Sub(session.CreatedAt)).
				Msg("Cleaned up old session")
		}
	}
}
