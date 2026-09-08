package browser

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// Regression test (review of PR #114, critical): CloseAll must not deadlock
// when a dirty named session exists. The original code called persistSession
// while holding sess.mu, and persistSession locks sess.mu itself (Go mutexes
// are not reentrant) — graceful shutdown hung forever. The deadlock happens
// before any CDP call, so no Chrome is needed here.
func TestCloseAllWithDirtySessionNoDeadlock(t *testing.T) {
	sm := &SessionManager{
		logger:     zerolog.Nop(),
		ttl:        30 * time.Minute,
		persistDir: t.TempDir(),
		stopCh:     make(chan struct{}),
		sessions: map[string]*namedSession{
			"dirty": {
				id:         "dirty",
				ctx:        context.Background(),
				cancel:     func() {},
				stateDirty: true,
			},
		},
	}

	done := make(chan struct{})
	go func() {
		sm.CloseAll()
		close(done)
	}()

	select {
	case <-done:
		// CloseAll returned — no deadlock.
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: CloseAll did not return within 5s with a dirty session")
	}
}
