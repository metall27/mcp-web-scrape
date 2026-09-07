package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Session state persistence (#107 stage 2): named sessions must survive
// process/container restarts so that anti-bot reputation accumulated on the
// target site (Ozon's abt_data / __Secure-ETC cookies, localStorage tokens,
// and the pinned browser identity) is NOT reset by every `docker compose up`.
//
// A snapshot is a JSON file per session id under SessionConfig.PersistDir:
//
//	{version, id, user_agent, fingerprint, created, last_access, saved_at,
//	 cookies[], local_storage{}}
//
// The file is written atomically (tmp+rename) with 0600 — cookie values are
// live credentials. On restart, GetOrCreate rehydrates the session from the
// file: identity is pinned FROM DISK (a cookie bound to one UA/fingerprint
// presented with another is itself an anomaly signal), and cookies are
// re-injected into the fresh browser context pre-navigation by the scraper
// (TakePendingCookies). Cookies are deliberately NOT injected inside
// GetOrCreate: any chromedp.Run there on a still-uninitialized context can
// poison the session (see the GetLocalStorage history in sessions.go).

const persistVersion = 1

// CookieState is the persisted form of one cookie of a named session.
// It mirrors network.Cookie fields that matter for a faithful restore
// (domain/path/flags — losing Secure or SameSite makes Chrome reject the
// cookie or the site treat it differently; __Secure- prefixed cookies
// REQUIRE Secure=true on set).
type CookieState struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"http_only"`
	Session  bool    `json:"session"`   // no expiry
	Expires  float64 `json:"expires"`   // unix seconds; 0 when Session
	SameSite string  `json:"same_site"` // "Strict" | "Lax" | "None" | ""
	Priority string  `json:"priority,omitempty"`
}

// persistedSession is the on-disk snapshot format (JSON).
type persistedSession struct {
	Version      int                `json:"version"`
	ID           string             `json:"id"`
	UserAgent    string             `json:"user_agent"`
	Fingerprint  BrowserFingerprint `json:"fingerprint"`
	Created      time.Time          `json:"created"`
	LastAccess   time.Time          `json:"last_access"`
	SavedAt      time.Time          `json:"saved_at"`
	Cookies      []CookieState      `json:"cookies"`
	LocalStorage map[string]string  `json:"local_storage,omitempty"`
}

// sessionFileName maps a session id to a safe file name. Ids that are
// already filesystem-safe keep their shape (debuggable); anything else is
// hashed. The ".json" suffix keeps the hash distinct from a literal
// sanitized id that happens to end in ".json".
func sessionFileName(id string) string {
	safe := id != "" && !strings.ContainsAny(id, "/\\") && !strings.Contains(id, "..")
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			safe = false
			break
		}
	}
	if safe && !strings.HasSuffix(strings.ToLower(id), ".json") {
		return id + ".json"
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8]) + ".json"
}

// saveStateToFile writes a snapshot atomically with 0600 perms.
func saveStateToFile(dir string, st persistedSession) error {
	if st.Version == 0 {
		st.Version = persistVersion
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("persist dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	final := filepath.Join(dir, sessionFileName(st.ID))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename session state: %w", err)
	}
	return nil
}

// loadStateFile reads a snapshot for id. Returns (nil, false) when no
// snapshot exists or it is unreadable/corrupt (corruption must never block
// session creation — the caller just starts fresh).
func loadStateFile(dir, id string) (*persistedSession, bool) {
	data, err := os.ReadFile(filepath.Join(dir, sessionFileName(id)))
	if err != nil {
		return nil, false
	}
	var st persistedSession
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, false
	}
	if st.Version != persistVersion || st.UserAgent == "" {
		return nil, false
	}
	return &st, true
}

// cookieStatesFromCDP reads the full cookie jar of the session's browser
// context (including HTTP-only cookies document.cookie cannot see).
func cookieStatesFromCDP(ctx context.Context) ([]CookieState, error) {
	var cookies []*network.Cookie
	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			cs, err := network.GetCookies().Do(ctx)
			cookies = cs
			return err
		}),
	); err != nil {
		return nil, err
	}
	out := make([]CookieState, 0, len(cookies))
	for _, c := range cookies {
		st := CookieState{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			Session:  c.Session,
			Priority: string(c.Priority),
		}
		if !c.Session && c.Expires > 0 {
			st.Expires = c.Expires
		}
		// SameSite zero value marshals as "" — keep it empty so restore skips it.
		st.SameSite = string(c.SameSite)
		out = append(out, st)
	}
	return out, nil
}

// enablePersistence turns on disk persistence for named sessions: snapshots
// are written when a session is closed/evicted, on shutdown, and every
// interval while the session is marked dirty (a scrape committed state).
// dir == "" disables everything. Called once from newSessionManager.
func (sm *SessionManager) enablePersistence(dir string, interval time.Duration) {
	if dir == "" {
		return
	}
	sm.mu.Lock()
	sm.persistDir = dir
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	sm.persistInterval = interval
	sm.dirty = make(map[string]bool)
	sm.mu.Unlock()

	go sm.persistLoop()
	sm.logger.Info().
		Str("persist_dir", dir).
		Dur("persist_interval", interval).
		Msg("Named session persistence enabled")
}

// MarkDirty records that a session's state (cookies/storage) changed since
// the last snapshot. The persistLoop flushes dirty sessions; Close/CloseAll
// flush dirty sessions before disposing the context.
func (sm *SessionManager) MarkDirty(id string) {
	sm.mu.Lock()
	if sm.persistDir == "" {
		sm.mu.Unlock()
		return
	}
	sm.dirty[id] = true
	sess, ok := sm.sessions[id]
	sm.mu.Unlock()
	if ok {
		sess.mu.Lock()
		sess.stateDirty = true
		sess.mu.Unlock()
	}
}

// persistLoop periodically flushes dirty sessions and prunes expired
// snapshots from disk. Stops when stopCh is closed.
func (sm *SessionManager) persistLoop() {
	interval := sm.persistInterval

	// Prune once at startup (not in the constructor — disk I/O + logging
	// belong to the goroutine), then on every tick.
	sm.pruneStaleSnapshots()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-sm.stopCh:
			return
		case <-ticker.C:
			sm.flushDirty()
			sm.pruneStaleSnapshots()
		}
	}
}

// flushDirty persists every session marked dirty. A dirty session that no
// longer exists (closed without persisting) is dropped from the map.
func (sm *SessionManager) flushDirty() {
	sm.mu.Lock()
	dirty := make([]string, 0, len(sm.dirty))
	for id := range sm.dirty {
		dirty = append(dirty, id)
	}
	sessions := make(map[string]*namedSession, len(sm.sessions))
	for id, s := range sm.sessions {
		sessions[id] = s
	}
	sm.mu.Unlock()

	for _, id := range dirty {
		sess, ok := sessions[id]
		if !ok {
			sm.mu.Lock()
			delete(sm.dirty, id)
			sm.mu.Unlock()
			continue
		}
		if err := sm.persistSession(sess); err != nil {
			sm.logger.Warn().Err(err).Str("session_id", id).
				Msg("Failed to persist named session state")
			continue
		}
		sm.mu.Lock()
		delete(sm.dirty, id)
		sm.mu.Unlock()
	}
}

// persistSession snapshots one session's cookies + storage + identity to
// disk. Runs CDP (network.GetCookies) against the session context — the
// allocator must still be alive (called before cancel in Close/CloseAll).
func (sm *SessionManager) persistSession(sess *namedSession) error {
	sm.mu.Lock()
	dir := sm.persistDir
	sm.mu.Unlock()
	if dir == "" {
		return nil
	}

	sess.mu.Lock()
	if !sess.stateDirty {
		// Nothing changed since the last snapshot — skip. This also protects
		// never-scraped sessions: their context has an empty jar, and the
		// chromedp.Run below would lazily LAUNCH Chrome just to read it.
		sess.mu.Unlock()
		return nil
	}
	created, lastAccess := sess.created, sess.lastAccess
	ua, fp := sess.userAgent, sess.fingerprint
	ls := make(map[string]string, len(sess.localStorageSnapshot))
	for k, v := range sess.localStorageSnapshot {
		ls[k] = v
	}
	sessCtx := sess.ctx
	sess.mu.Unlock()

	cookies, err := cookieStatesFromCDP(sessCtx)
	if err != nil {
		return fmt.Errorf("collect cookies: %w", err)
	}

	err = saveStateToFile(dir, persistedSession{
		Version:      persistVersion,
		ID:           sess.id,
		UserAgent:    ua,
		Fingerprint:  fp,
		Created:      created,
		LastAccess:   lastAccess,
		SavedAt:      time.Now().UTC(),
		Cookies:      cookies,
		LocalStorage: ls,
	})
	if err == nil {
		// Snapshot on disk is current again.
		sess.mu.Lock()
		sess.stateDirty = false
		sess.mu.Unlock()
	}
	return err
}

// pruneStaleSnapshots deletes persisted files older than the inactivity TTL,
// mirroring the in-memory eviction so disk does not accumulate dead
// reputations. When TTL <= 0 (eviction disabled) files are kept forever —
// the whole point of #107 is week-scale reputation.
func (sm *SessionManager) pruneStaleSnapshots() {
	sm.mu.Lock()
	dir := sm.persistDir
	ttl := sm.ttl
	active := make(map[string]bool, len(sm.sessions))
	for id := range sm.sessions {
		active[sessionFileName(id)] = true
	}
	sm.mu.Unlock()
	if dir == "" || ttl <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Never prune a file belonging to an ACTIVE session — its live
		// lastAccess is fresher than the file's mtime would suggest.
		if active[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) <= ttl {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			sm.logger.Info().Str("file", e.Name()).
				Msg("Pruned stale persisted session snapshot (TTL exceeded)")
		}
	}
}
