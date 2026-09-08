package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestSessionFileName(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"ozon", "ozon.json"},
		{"my_session-1", "my_session-1.json"},
		{"a.b.c", "a.b.c.json"},
		// unsafe ids must be hashed, never contain path separators
		{"../etc/passwd", ""},
		{"a/b", ""},
		{"", ""},
		// a literal id ending in .json hashes to avoid collision with the
		// file of the session "x"
		{"x.json", ""},
	}
	for _, tc := range cases {
		got := sessionFileName(tc.id)
		if tc.want != "" {
			if got != tc.want {
				t.Errorf("sessionFileName(%q) = %q, want %q", tc.id, got, tc.want)
			}
			continue
		}
		if got == "" || got == ".json" {
			t.Errorf("sessionFileName(%q) = %q — bad name", tc.id, got)
		}
		if filepath.Base(got) != got {
			t.Errorf("sessionFileName(%q) = %q — escaped dir", tc.id, got)
		}
	}
	// "x.json" and "x" must not collide
	if sessionFileName("x") == sessionFileName("x.json") {
		t.Error("collision between 'x' and 'x.json'")
	}
	// distinct unsafe ids get distinct names
	if sessionFileName("a/b") == sessionFileName("a\\b") {
		t.Error("collision between distinct unsafe ids")
	}
}

func TestSaveLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	st := persistedSession{
		ID:          "ozon",
		UserAgent:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
		Fingerprint: BrowserFingerprint{Timezone: "Europe/Moscow", Language: "ru-RU", Platform: "Win32"},
		Created:     now,
		LastAccess:  now,
		SavedAt:     now,
		Cookies: []CookieState{
			{Name: "abt_data", Value: "tok", Domain: ".ozon.ru", Path: "/", Secure: true, HTTPOnly: true, Expires: float64(now.Add(24 * time.Hour).Unix()), SameSite: "Lax"},
			{Name: "sess", Value: "v", Domain: ".ozon.ru", Path: "/", Session: true},
		},
		LocalStorage: map[string]string{"k": "v"},
	}
	if err := saveStateToFile(dir, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 0600 — the file holds live credentials
	info, err := os.Stat(filepath.Join(dir, "ozon.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perms = %o, want 600", perm)
	}

	got, ok := loadStateFile(dir, "ozon")
	if !ok {
		t.Fatal("load: not found")
	}
	if got.UserAgent != st.UserAgent || got.Fingerprint.Timezone != "Europe/Moscow" {
		t.Error("identity not round-tripped")
	}
	if len(got.Cookies) != 2 || got.Cookies[0].Name != "abt_data" || !got.Cookies[0].Secure || got.Cookies[0].SameSite != "Lax" {
		t.Errorf("cookies not round-tripped: %+v", got.Cookies)
	}
	if !got.Cookies[1].Session {
		t.Error("session flag lost")
	}
	if got.LocalStorage["k"] != "v" {
		t.Error("localStorage not round-tripped")
	}
	if got.Version != persistVersion {
		t.Errorf("version = %d, want %d", got.Version, persistVersion)
	}
}

func TestLoadStateFileRobustness(t *testing.T) {
	dir := t.TempDir()

	// missing
	if _, ok := loadStateFile(dir, "nope"); ok {
		t.Error("missing file should not load")
	}

	// corrupt JSON
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o600)
	if _, ok := loadStateFile(dir, "bad"); ok {
		t.Error("corrupt file should not load")
	}

	// wrong version
	os.WriteFile(filepath.Join(dir, "ver.json"),
		[]byte(`{"version":99,"user_agent":"ua"}`), 0o600)
	if _, ok := loadStateFile(dir, "ver"); ok {
		t.Error("wrong version should not load")
	}

	// no UA — without an identity a reputation snapshot is meaningless
	os.WriteFile(filepath.Join(dir, "noua.json"),
		[]byte(`{"version":1,"user_agent":""}`), 0o600)
	if _, ok := loadStateFile(dir, "noua"); ok {
		t.Error("empty UA should not load")
	}
}

func TestMarkDirtyNoopWithoutPersistence(t *testing.T) {
	sm := &SessionManager{
		logger:   zerolog.Nop(),
		sessions: map[string]*namedSession{},
	}
	sm.MarkDirty("x") // must not panic with persistence off
	if len(sm.dirty) != 0 {
		t.Error("dirty map should stay empty when persistence is off")
	}
}

func TestFlushDirtyDropsUnknownSession(t *testing.T) {
	sm := &SessionManager{
		logger:     zerolog.Nop(),
		sessions:   map[string]*namedSession{},
		persistDir: t.TempDir(),
		dirty:      map[string]bool{"ghost": true},
	}
	sm.flushDirty()
	if len(sm.dirty) != 0 {
		t.Errorf("dirty ghost should be dropped, got %v", sm.dirty)
	}
}

func TestTakePendingCookies(t *testing.T) {
	sm := &SessionManager{
		logger:   zerolog.Nop(),
		sessions: map[string]*namedSession{},
	}
	// unknown session
	if got := sm.PeekPendingCookies("ghost"); got != nil {
		t.Error("unknown session should return nil")
	}

	cookies := []CookieState{{Name: "abt_data", Domain: ".ozon.ru"}}
	sm.sessions["s"] = &namedSession{id: "s", pendingCookies: cookies}

	got := sm.PeekPendingCookies("s")
	if len(got) != 1 || got[0].Name != "abt_data" {
		t.Fatalf("pending cookies not returned: %+v", got)
	}
	// peek does NOT clear — retry attempts must be able to re-read
	if got := sm.PeekPendingCookies("s"); got == nil {
		t.Error("peek must not clear the pending queue")
	}
	// explicit clear is the one-shot point (injection time)
	sm.ClearPendingCookies("s")
	if got := sm.PeekPendingCookies("s"); got != nil {
		t.Error("pending cookies should be cleared after ClearPendingCookies")
	}
}

func TestPruneStaleSnapshots(t *testing.T) {
	dir := t.TempDir()
	logger := zerolog.Nop()

	// A snapshot with an old mtime, an active session's file, and a fresh one.
	old := persistedSession{ID: "dead", UserAgent: "ua", SavedAt: time.Now().UTC()}
	if err := saveStateToFile(dir, old); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(dir, "dead.json"), stale, stale)

	if err := saveStateToFile(dir, persistedSession{ID: "alive", UserAgent: "ua"}); err != nil {
		t.Fatal(err)
	}
	oldFresh := time.Now().Add(-time.Minute)
	os.Chtimes(filepath.Join(dir, "alive.json"), oldFresh, oldFresh)

	sm := &SessionManager{
		logger:     logger,
		ttl:        30 * time.Minute,
		persistDir: dir,
		sessions:   map[string]*namedSession{"alive": {id: "alive"}},
	}
	sm.pruneStaleSnapshots()

	if _, err := os.Stat(filepath.Join(dir, "dead.json")); !os.IsNotExist(err) {
		t.Error("stale snapshot should be pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "alive.json")); err != nil {
		t.Error("active session snapshot must survive pruning")
	}
}

func TestPruneDisabledWithZeroTTL(t *testing.T) {
	dir := t.TempDir()
	if err := saveStateToFile(dir, persistedSession{ID: "x", UserAgent: "ua"}); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-1000 * time.Hour)
	os.Chtimes(filepath.Join(dir, "x.json"), stale, stale)

	sm := &SessionManager{logger: zerolog.Nop(), ttl: 0, persistDir: dir, sessions: map[string]*namedSession{}}
	sm.pruneStaleSnapshots()
	if _, err := os.Stat(filepath.Join(dir, "x.json")); err != nil {
		t.Error("TTL=0 must keep snapshots forever (#107 week-scale reputation)")
	}
}

// The persisted file must be valid JSON with stable field names — it is an
// on-disk contract read across versions.
func TestPersistedJSONShape(t *testing.T) {
	dir := t.TempDir()
	st := persistedSession{ID: "s", UserAgent: "ua", SavedAt: time.Now().UTC()}
	if err := saveStateToFile(dir, st); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "s.json"))
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "id", "user_agent", "fingerprint", "saved_at", "cookies"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing field %q in persisted JSON", key)
		}
	}
}

// GetOrCreate must pin the identity FROM DISK when a snapshot exists —
// the whole reputation premise (#107): cookies correlated with a UA must
// never be presented under a rotated identity.
func TestGetOrCreateRehydratesPersistedIdentity(t *testing.T) {
	dir := t.TempDir()
	savedUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	savedFP := BrowserFingerprint{Timezone: "Europe/Moscow", Language: "ru-RU", Platform: "Win32"}
	cookies := []CookieState{{Name: "abt_data", Value: "tok", Domain: ".ozon.ru", Path: "/", Secure: true}}
	ls := map[string]string{"token": "abc"}

	if err := saveStateToFile(dir, persistedSession{
		ID: "ozon", UserAgent: savedUA, Fingerprint: savedFP,
		Cookies: cookies, LocalStorage: ls, SavedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	pool, err := New(Config{Logger: zerolog.Nop(), MaxTabs: 2, Headless: true, NoSandbox: true, SessionTTL: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	sm := pool.Sessions()
	sm.mu.Lock()
	sm.persistDir = dir
	sm.mu.Unlock()

	// Caller passes a DIFFERENT fresh identity — the snapshot must win.
	if _, err := sm.GetOrCreate(context.Background(), "ozon", "Mozilla/5.0 (Brand-New)", BrowserFingerprint{Platform: "MacIntel"}); err != nil {
		t.Fatal(err)
	}

	ua, ok := sm.GetUserAgent("ozon")
	if !ok || ua != savedUA {
		t.Errorf("UA = %q, want persisted %q", ua, savedUA)
	}
	fp, _ := sm.GetFingerprint("ozon")
	if fp.Platform != "Win32" || fp.Timezone != "Europe/Moscow" {
		t.Errorf("fingerprint not restored: %+v", fp)
	}
	if got := sm.GetCachedLocalStorage("ozon"); got["token"] != "abc" {
		t.Errorf("localStorage not restored: %v", got)
	}
	pc := sm.PeekPendingCookies("ozon")
	if len(pc) != 1 || pc[0].Name != "abt_data" {
		t.Errorf("pending cookies not staged: %+v", pc)
	}
	// Peek stays available until the scraper injects and clears — the
	// Phase 5 retry loop rebuilds the scrape context per attempt and must
	// not lose rehydrated cookies to a one-shot take (review #114).
	if pc := sm.PeekPendingCookies("ozon"); pc == nil {
		t.Error("pending cookies must survive a peek (cleared only at injection)")
	}
	sm.ClearPendingCookies("ozon")
	if pc := sm.PeekPendingCookies("ozon"); pc != nil {
		t.Error("pending cookies should be cleared after injection")
	}
}
