package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
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

	// localStorageSnapshot is the last successfully captured localStorage
	// for this session, populated by SaveLocalStorage after a scrape
	// navigation completes. GetCachedLocalStorage returns it so the next
	// scrape can re-inject the values pre-navigation via
	// AddScriptToEvaluateOnNewDocument (#59). This is a pure map read — it
	// NEVER issues a CDP round-trip, which avoids the about:blank
	// SecurityError + session-context poisoning that the original live
	// GetLocalStorage caused on fresh/reused sessions. Guarded by mu.
	localStorageSnapshot map[string]string

	// pendingCookies holds cookies rehydrated from a persisted snapshot
	// (#107 stage 2) that have NOT yet been injected into the live browser
	// context. TakePendingCookies pops them. Injection happens in the
	// scraper's pre-navigation task (the same ActionFunc that applies the
	// UA override — proven-safe on a fresh session context), NEVER inside
	// GetOrCreate, where any chromedp.Run could poison the context.
	// Guarded by mu.
	pendingCookies []CookieState

	// stateDirty is set by MarkDirty after a scrape committed new state
	// (cookies/storage). Close/CloseAll persist ONLY dirty sessions:
	// persisting a never-used session would lazily LAUNCH Chrome just to
	// read an empty cookie jar at shutdown. Guarded by mu.
	stateDirty bool
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

	// Persistence (#107 stage 2): when persistDir != "", session state
	// (cookies, localStorage, pinned identity) is snapshotted to disk and
	// rehydrated on next creation, so reputation survives restarts.
	// dirty tracks sessions whose state changed since the last flush.
	persistDir      string
	persistInterval time.Duration
	dirty           map[string]bool
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
	persistDir := sm.persistDir
	sm.mu.Unlock()

	// #107 stage 2: rehydrate from a persisted snapshot BEFORE creating the
	// context. A warm reputation is worthless if the identity that earned
	// it changed — cookies are correlated with UA/fingerprint — so the
	// persisted identity wins over the caller's freshly generated one.
	var (
		restored        *persistedSession
		hadSnap         bool
		restoredLS      map[string]string
		restoredCookies []CookieState
	)
	if persistDir != "" {
		restored, hadSnap = loadStateFile(persistDir, id)
		if hadSnap {
			userAgent = restored.UserAgent
			fingerprint = restored.Fingerprint
			restoredLS = restored.LocalStorage
			restoredCookies = restored.Cookies
			sm.logger.Info().
				Str("session_id", id).
				Str("user_agent", userAgent).
				Time("saved_at", restored.SavedAt).
				Int("cookies", len(restoredCookies)).
				Msg("Rehydrating named session from persisted state")
		}
	}

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
	if hadSnap {
		// The snapshot's localStorage becomes the session's live snapshot
		// immediately — the scraper's #59 pre-navigation injection then
		// re-seeds it before the first page JS runs, with no CDP round-trip.
		sess.localStorageSnapshot = restoredLS
		// Cookies are NOT injected here (a chromedp.Run on the still
		// uninitialized context can poison it — see GetCachedLocalStorage
		// history). They wait in pendingCookies for the scraper's
		// pre-navigation task.
		sess.pendingCookies = restoredCookies
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

// SetUserAgent updates the User-Agent pinned to a named session. Used when
// ua-sync (#105) rewrites the Chrome major of a rehydrated pinned UA after an
// engine upgrade — the persisted identity converges to the engine's actual
// major so the session never advertises a version the engine betrays.
func (sm *SessionManager) SetUserAgent(id, ua string) {
	if ua == "" {
		return
	}
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	sm.mu.Unlock()
	if !ok {
		return
	}
	sess.mu.Lock()
	if sess.userAgent != ua {
		sess.userAgent = ua
		sess.stateDirty = true // persist the converged identity
	}
	sess.mu.Unlock()
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

// GetCachedLocalStorage returns the last captured localStorage snapshot for a
// named session, or nil when the session does not exist or no snapshot has
// been captured yet.
//
// This is a PURE MAP READ — it never issues a CDP round-trip. The snapshot is
// populated by SaveLocalStorage, which the scraper must call from INSIDE the
// navigation chromedp.Run (after the page has loaded), where localStorage is
// accessible. The next scrape then reads the snapshot here and re-injects it
// pre-navigation via page.AddScriptToEvaluateOnNewDocument so SPA frameworks
// that hydrate auth state from localStorage (Zustand persist, Redux persist)
// see the token synchronously on first JS execution (#59).
//
// The earlier live GetLocalStorage implementation ran its own chromedp.Run on
// the shared session context BEFORE navigation. On a fresh or reused session
// the tab sits at about:blank, where window.localStorage is a SecurityError —
// and that failed Run poisoned the session context so every subsequent CDP
// op (UA override, stealth, Navigate) returned "context canceled" across all
// retry attempts until the HTTP fallback kicked in. Reading a cached map
// instead eliminates that entire failure path.
func (sm *SessionManager) GetCachedLocalStorage(id string) map[string]string {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	if ok {
		sess.touch()
	}
	sm.mu.Unlock()
	if !ok {
		return nil
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.localStorageSnapshot) == 0 {
		return nil
	}
	// Return a copy so callers cannot mutate the session's snapshot.
	out := make(map[string]string, len(sess.localStorageSnapshot))
	for k, v := range sess.localStorageSnapshot {
		out[k] = v
	}
	return out
}

// SaveLocalStorage stores a localStorage snapshot on a named session for later
// retrieval via GetCachedLocalStorage. values may be nil (clears the snapshot).
// It is a pure in-memory write and does not touch Chrome.
func (sm *SessionManager) SaveLocalStorage(id string, values map[string]string) {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	sm.mu.Unlock()
	if !ok {
		return
	}

	sess.mu.Lock()
	sess.localStorageSnapshot = values
	sess.mu.Unlock()
}

// PeekPendingCookies returns the cookies restored from a persisted snapshot
// that have not yet been injected into the live browser context (#107 stage 2)
// WITHOUT clearing the queue. The scraper reads them in createScrapeContext
// (every retry attempt re-reads — the Phase 5 loop rebuilds the scrape
// context, and a one-shot take here would lose the cookies forever if the
// first attempt failed before injection). The queue is cleared by
// ClearPendingCookies from the pre-navigation ActionFunc — the same context
// where the UA override is applied, the first chromedp.Run of the session.
// The named-session context survives across retry attempts (browserCancel is
// a no-op), so once injected the cookies stay in the jar; clearing exactly at
// injection time is the correct one-shot point.
// Returns nil when the session is unknown or has no pending cookies.
func (sm *SessionManager) PeekPendingCookies(id string) []CookieState {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	sm.mu.Unlock()
	if !ok {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.pendingCookies == nil {
		return nil
	}
	out := make([]CookieState, len(sess.pendingCookies))
	copy(out, sess.pendingCookies)
	return out
}

// ClearPendingCookies drops the pending rehydrated-cookie queue after the
// scraper injected them into the live browser context (#107 stage 2).
func (sm *SessionManager) ClearPendingCookies(id string) {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	sm.mu.Unlock()
	if !ok {
		return
	}
	sess.mu.Lock()
	sess.pendingCookies = nil
	sess.mu.Unlock()
}

// CookieInfo holds the metadata of a single cookie for session inspection (#42).
// The cookie Value is omitted by default (includeValues=false) because session
// cookies are sensitive credentials — the debug use case ("is the cookie
// present? when does it expire?") rarely needs the value, and dumping it
// wholesale into an LLM context risks leaking auth tokens.
type CookieInfo struct {
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	HTTPOnly bool   `json:"http_only"`
	Secure   bool   `json:"secure"`
	Session  bool   `json:"session"` // true = session cookie (no expiry)
	// Expires as a human-readable RFC3339 timestamp. Empty for session cookies.
	Expires string `json:"expires,omitempty"`
	// Value is only populated when includeValues=true.
	Value string `json:"value,omitempty"`
}

// StorageDump holds the keys (and optionally values) of localStorage and
// sessionStorage for the session's browser context.
type StorageDump struct {
	// Keys are the storage entry keys. Values are omitted unless includeValues.
	Keys []string `json:"keys"`
	// Values is keyed by storage key; only populated when includeValues=true.
	Values map[string]string `json:"values,omitempty"`
}

// SessionDump is the full inspection result for a named session (#42).
// It exposes what the session actually holds — cookies (including HTTP-only,
// which document.cookie cannot see) and the Web Storage keys — so a caller
// debugging a login-gated workflow can answer "am I actually logged in?"
type SessionDump struct {
	SessionID      string       `json:"session_id"`
	Created        time.Time    `json:"created"`
	LastAccess     time.Time    `json:"last_access"`
	UserAgent      string       `json:"user_agent"`
	CookieCount    int          `json:"cookie_count"`
	Cookies        []CookieInfo `json:"cookies"`
	LocalStorage   StorageDump  `json:"local_storage"`
	SessionStorage StorageDump  `json:"session_storage"`
}

// Dump inspects a named session and returns its cookies and Web Storage keys
// for debugging (#42). It runs CDP commands (network.GetAllCookies +
// chromedp.Evaluate) against the session's persistent browser context, so it
// sees HTTP-only cookies that document.cookie cannot reach.
//
// includeValues controls whether sensitive values are returned: when false
// (the default/recommended), cookie Value fields and storage Values are omitted
// — only metadata + keys. Set true only for deep debugging where you need the
// actual token (it then enters the MCP response and potentially an LLM context).
//
// Returns (nil, false) when the session does not exist or named sessions are
// disabled. The ctx is used as the parent for chromedp.Run; pass a context
// with a reasonable timeout (e.g. 15s) — Chrome must be reachable.
func (sm *SessionManager) Dump(ctx context.Context, id string, includeValues bool) (*SessionDump, bool) {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	if ok {
		sess.touch()
	}
	sm.mu.Unlock()
	if !ok {
		return nil, false
	}

	sess.mu.Lock()
	created := sess.created
	lastAccess := sess.lastAccess
	ua := sess.userAgent
	sessCtx := sess.ctx
	sess.mu.Unlock()

	dump := &SessionDump{
		SessionID:  id,
		Created:    created,
		LastAccess: lastAccess,
		UserAgent:  ua,
	}

	// Collect cookies via CDP. GetCookies sees the full cookie jar of the
	// browser context, including HTTP-only cookies (inaccessible from JS).
	// It returns (cookies, err) directly from Do, so run it inside an
	// ActionFunc to keep it within chromedp.Run.
	var cookies []*network.Cookie
	if err := chromedp.Run(sessCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			cs, err := network.GetCookies().Do(ctx)
			cookies = cs
			return err
		}),
	); err != nil {
		sm.logger.Warn().Err(err).Str("session_id", id).Msg("session dump: failed to get cookies")
		// Continue — storage may still be readable.
	}

	dump.CookieCount = len(cookies)
	dump.Cookies = make([]CookieInfo, 0, len(cookies))
	for _, c := range cookies {
		info := CookieInfo{
			Name:     c.Name,
			Domain:   c.Domain,
			Path:     c.Path,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
			Session:  c.Session,
		}
		if !c.Session && c.Expires > 0 {
			info.Expires = time.Unix(int64(c.Expires), 0).UTC().Format(time.RFC3339)
		}
		if includeValues {
			info.Value = c.Value
		}
		dump.Cookies = append(dump.Cookies, info)
	}

	// Collect localStorage / sessionStorage keys via JS. A single Evaluate
	// collects both to avoid two round-trips. Values are optional.
	type storageProbe struct {
		LocalKeys     []string          `json:"localKeys"`
		SessionKeys   []string          `json:"sessionKeys"`
		LocalValues   map[string]string `json:"localValues,omitempty"`
		StorageValues map[string]string `json:"storageValues,omitempty"`
	}
	var probe storageProbe
	js := `(() => {
		const out = { localKeys: [], sessionKeys: [] };
		for (let i = 0; i < localStorage.length; i++)
			out.localKeys.push(localStorage.key(i));
		for (let i = 0; i < sessionStorage.length; i++)
			out.sessionKeys.push(sessionStorage.key(i));
		return out;
	})()`
	// When values requested, augment the script to capture them too.
	if includeValues {
		js = `(() => {
			const out = { localKeys: [], sessionKeys: [], localValues: {}, storageValues: {} };
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				out.localKeys.push(k);
				out.localValues[k] = localStorage.getItem(k);
			}
			for (let i = 0; i < sessionStorage.length; i++) {
				const k = sessionStorage.key(i);
				out.sessionKeys.push(k);
				out.storageValues[k] = sessionStorage.getItem(k);
			}
			return out;
		})()`
	}
	if err := chromedp.Run(sessCtx,
		chromedp.Evaluate(js, &probe),
	); err != nil {
		sm.logger.Warn().Err(err).Str("session_id", id).Msg("session dump: failed to read storage")
	}

	dump.LocalStorage = StorageDump{Keys: probe.LocalKeys}
	if includeValues {
		dump.LocalStorage.Values = probe.LocalValues
	}
	dump.SessionStorage = StorageDump{Keys: probe.SessionKeys}
	if includeValues {
		dump.SessionStorage.Values = probe.StorageValues
	}

	return dump, true
}

// List returns metadata for every active named session (without cookie/storage
// contents). Useful for a "which sessions exist?" overview before Dump-ing one.
func (sm *SessionManager) List() []SessionDump {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	out := make([]SessionDump, 0, len(sm.sessions))
	for id, sess := range sm.sessions {
		sess.mu.Lock()
		out = append(out, SessionDump{
			SessionID:  id,
			Created:    sess.created,
			LastAccess: sess.lastAccess,
			UserAgent:  sess.userAgent,
		})
		sess.mu.Unlock()
	}
	return out
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
	dirty := sess.stateDirty
	sess.mu.Unlock()

	// Persist the final state BEFORE cancelling the context — the cookie
	// dump needs the live browser context (#107 stage 2). Only dirty
	// sessions: a never-scraped session has an empty jar, and persisting it
	// would lazily launch Chrome during shutdown.
	if sm.persistDir != "" && dirty {
		if err := sm.persistSession(sess); err != nil {
			sm.logger.Warn().Err(err).Str("session_id", id).
				Msg("Failed to persist session state on close")
		}
	}

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
		closed := sess.closed
		if !closed {
			sess.closed = true
		}
		// Persist dirty sessions before cancelling — the cookie dump
		// needs the live context; this is the graceful-shutdown path
		// that makes a `docker compose up -d` restart reputation-neutral
		// (#107). Non-dirty sessions are skipped: persisting them would
		// lazily launch Chrome to read an empty jar.
		//
		// NOTE: persistSession locks sess.mu itself — read the dirty flag
		// under the lock and RELEASE it before persisting (same pattern
		// as Close; locking recursively would deadlock on the
		// non-reentrant mutex).
		dirty := sess.stateDirty
		sess.mu.Unlock()

		if !closed {
			if sm.persistDir != "" && dirty {
				if err := sm.persistSession(sess); err != nil {
					sm.logger.Warn().Err(err).Str("session_id", id).
						Msg("Failed to persist session state on shutdown")
				}
			}
			sess.cancel()
		}
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
