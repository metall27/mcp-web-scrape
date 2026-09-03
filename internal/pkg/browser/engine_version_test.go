package browser

import (
	"fmt"
	"strings"
	"testing"
)

// TestSyncUserAgentToEngine (#105 ua-sync): the Chrome major in a UA string
// must be rewritten to the engine's actual major; everything else preserved.
func TestSyncUserAgentToEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool, err := New(Config{MaxTabs: 1, Headless: true, NoSandbox: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	major := pool.EngineChromeMajor()
	if major == 0 {
		t.Skip("engine version detection failed in this environment")
	}
	t.Logf("engine major: %d", major)

	cases := []struct{ name, in string }{
		{"old chrome win", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"},
		{"old chrome mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"},
		{"already synced", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + fmt.Sprintf("%d", major) + ".0.0.0 Safari/537.36"},
	}
	for _, c := range cases {
		out := pool.SyncUserAgentToEngine(c.in)
		if !strings.Contains(out, "Chrome/"+fmt.Sprintf("%d", major)+".") {
			t.Errorf("%s: major not synced: %s", c.name, out)
		}
		// platform tokens preserved
		if strings.Contains(c.in, "Windows NT") && !strings.Contains(out, "Windows NT") {
			t.Errorf("%s: platform token lost: %s", c.name, out)
		}
		if strings.Contains(c.in, "Macintosh") && !strings.Contains(out, "Macintosh") {
			t.Errorf("%s: platform token lost: %s", c.name, out)
		}
	}

	// Non-Chrome UA passes through untouched.
	firefox := "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0"
	if got := pool.SyncUserAgentToEngine(firefox); got != firefox {
		t.Errorf("firefox UA must pass through unchanged, got %s", got)
	}
	// Empty passes through.
	if got := pool.SyncUserAgentToEngine(""); got != "" {
		t.Errorf("empty UA must stay empty, got %s", got)
	}
}
