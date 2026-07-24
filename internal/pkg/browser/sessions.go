package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

// namedSession holds a persistent browser context that survives across
// multiple scrape_with_js calls. All tabs created within this context
// share the same cookie jar, localStorage and sessionStorage, enabling
// login-gated workflows (authenticate once, then fetch N pages without
// re-logging-in).
//
// The userAgent and fingerprint are pinned at creation time so that every
// subsequent scrape within this session presents the same browser identity
// to the target site. Sites that correlate cookies with JA3/JA4 TLS
// fingerprint, User-Agent, navigator.platform, timezone or WebGL renderer
// would otherwise flag the session as anomalous when each call rotates a
// different identity (#41).
type namedSession struct {
	id         string
	ctx        context.Context // persistent chromedp context (owns the BrowserContext)
	cancel     context.CancelFunc
	created    time.Time
	lastAccess time.Time
	mu         sync.Mutex // guards lastAccess updates + close-once semantics
	closed     bool

	// Pinned browser identity — set once when the session is created,
	// reused by every subsequent scrape in this session.
	userAgent   string
	fingerprint BrowserFingerprint
}

func (s *namedSession) touch() {
	s.mu.Lock()
	s.lastAccess = time.Now()
	s.mu.Unlock()
}

// SessionManager owns the lifecycle of named persistent browser sessions.
// It is embedded in Pool and guarded by Pool.mu.
type SessionManager struct {
	pool     *Pool
	logger   zerolog.Logger
	ttl      time.Duration
	mu       sync.Mutex
	sessions map[string]*namedSession
	stopOnce sync.Once
	stopCh   chan struct{}
}

func newSessionManager(pool *Pool, logger zerolog.Logger, ttl time.Duration) *SessionManager {
	sm := &SessionManager{
		pool:     pool,
		logger:   logger,
		ttl:      ttl,
		sessions: make(map[string]*namedSession),
		stopCh:   make(chan struct{}),
	}
	if ttl > 0 {
		go sm.cleanupLoop()
	}
	return sm
}

// GetOrCreate returns an existing named session, or creates a new one.
// The returned context is the persistent chromedp browser context —
// navigations happen directly in it across multiple scrape calls.
//
// userAgent and fingerprint are pinned when the session is first created
// and ignored on subsequent calls (the existing session keeps its original
// identity). This keeps the browser identity stable for the lifetime of a
// named session (#41): a site that ties cookies to UA/JA3/navigator will
// see a consistent browser across every scrape.
func (sm *SessionManager) GetOrCreate(parent context.Context, id, userAgent string, fingerprint BrowserFingerprint) (context.Context, error) {
	sm.mu.Lock()
	if sess, ok := sm.sessions[id]; ok {
		sess.touch()
		sm.mu.Unlock()
		sm.logger.Debug().Str("session_id", id).Msg("Reusing existing named session")
		return sess.ctx, nil
	}
	sm.mu.Unlock()

	// Create a new session outside the lock — browser context creation
	// can take seconds (Chrome launch). Other sessions remain usable.
	sm.logger.Info().Str("session_id", id).Msg("Creating new named session")

	// Derive a tab context from the pool's allocator. This launches (or
	// reuses) the browser and establishes a new browser context via CDP.
	// chromedp.NewContext with a fresh parent gives us a tab that owns a
	// BrowserContext; cancelling this context disposes it. We must NOT
	// cancel it per-scrape — only on Close/eviction.
	taskCtx, cancel := chromedp.NewContext(parent,
		chromedp.WithErrorf(func(format string, v ...interface{}) {
			msg := fmt.Sprintf(format, v...)
			if !shouldLogChromedpError(msg) {
				return
			}
			sm.logger.Error().Str("source", "chromedp").Str("session_id", id).Msg(msg)
		}),
		chromedp.WithLogf(func(format string, v ...interface{}) {
			msg := fmt.Sprintf(format, v...)
			if !shouldLogChromedpError(msg) {
				return
			}
			sm.logger.Debug().Str("source", "chromedp").Str("session_id", id).Msg(msg)
		}),
	)

	// NOTE: No pre-initialization here. The browser/tab is initialized
	// lazily by the first chromedp.Run in scrapeAttempt — exactly like the
	// ephemeral path. Pre-initializing with a child context caused
	// "context canceled" errors when the child timeout fired.
	sess := &namedSession{
		id:          id,
		ctx:         taskCtx,
		cancel:      cancel,
		created:     time.Now(),
		lastAccess:  time.Now(),
		userAgent:   userAgent,
		fingerprint: fingerprint,
	}

	sm.mu.Lock()
	// Re-check: another goroutine may have created the same session concurrently.
	if existing, ok := sm.sessions[id]; ok {
		sm.mu.Unlock()
		// Discard the session we just created; reuse the existing one.
		cancel()
		existing.touch()
		return existing.ctx, nil
	}
	sm.sessions[id] = sess
	sm.mu.Unlock()

	return taskCtx, nil
}

// GetUserAgent returns the User-Agent pinned to a named session.
// Returns "" and false when the session does not exist. The caller uses
// this to override the per-call random UA so every scrape within a session
// advertises the same UA to the target site.
func (sm *SessionManager) GetUserAgent(id string) (string, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sess, ok := sm.sessions[id]
	if !ok {
		return "", false
	}
	return sess.userAgent, true
}

// GetFingerprint returns the BrowserFingerprint pinned to a named session.
// Returns a zero BrowserFingerprint and false when the session does not
// exist. The caller uses this so stealth injection (timezone, language,
// platform, WebGL) stays consistent across every scrape in the session.
func (sm *SessionManager) GetFingerprint(id string) (BrowserFingerprint, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sess, ok := sm.sessions[id]
	if !ok {
		return BrowserFingerprint{}, false
	}
	return sess.fingerprint, true
}

// Close closes a named session by id, disposing its browser context and
// removing it from the manager. Returns false if the session did not exist.
func (sm *SessionManager) Close(id string) bool {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return false
	}
	delete(sm.sessions, id)
	sm.mu.Unlock()

	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return true
	}
	sess.closed = true
	sess.mu.Unlock()

	sess.cancel()
	sm.logger.Info().Str("session_id", id).Msg("Named session closed")
	return true
}

// CloseAll disposes every named session. Called during shutdown.
func (sm *SessionManager) CloseAll() {
	sm.stopOnce.Do(func() {
		close(sm.stopCh)
	})

	sm.mu.Lock()
	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	sessions := sm.sessions
	sm.sessions = make(map[string]*namedSession)
	sm.mu.Unlock()

	for _, id := range ids {
		sess := sessions[id]
		sess.mu.Lock()
		if !sess.closed {
			sess.closed = true
			sess.cancel()
		}
		sess.mu.Unlock()
	}
	if len(ids) > 0 {
		sm.logger.Info().Int("count", len(ids)).Msg("Closed all named sessions")
	}
}

// Stats returns session manager statistics for the metrics endpoint.
func (sm *SessionManager) Stats() map[string]interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return map[string]interface{}{
		"active_sessions": len(sm.sessions),
		"ttl_seconds":     int(sm.ttl.Seconds()),
		"enabled":         sm.ttl > 0,
	}
}

// cleanupLoop periodically evicts sessions that have been inactive longer
// than the configured TTL. Stops when stopCh is closed (shutdown).
func (sm *SessionManager) cleanupLoop() {
	// Check every minute, or every TTL/4 — whichever is smaller, but at
	// least every 30s.
	interval := sm.ttl / 4
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if interval > time.Minute {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopCh:
			return
		case <-ticker.C:
			sm.evictExpired()
		}
	}
}

// evictExpired closes sessions whose lastAccess is older than TTL.
func (sm *SessionManager) evictExpired() {
	now := time.Now()
	var expired []string

	sm.mu.Lock()
	for id, sess := range sm.sessions {
		sess.mu.Lock()
		last := sess.lastAccess
		sess.mu.Unlock()
		if now.Sub(last) > sm.ttl {
			expired = append(expired, id)
		}
	}
	sm.mu.Unlock()

	for _, id := range expired {
		if sm.Close(id) {
			sm.logger.Info().
				Str("session_id", id).
				Dur("ttl", sm.ttl).
				Msg("Named session evicted (inactivity TTL exceeded)")
		}
	}
}
