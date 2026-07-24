package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/rs/zerolog"
)

// TestSessionInfoToolDisabled verifies the tool reports clearly when named
// sessions are disabled (nil pool), instead of panicking. This is the
// most common real-world failure: SessionTTL=0 by default.
func TestSessionInfoToolDisabled(t *testing.T) {
	tool := NewSessionInfoTool(nil) // nil pool = sessions disabled

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"session_id": "anything",
	})
	if err == nil {
		t.Fatal("expected error when browser pool is nil, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention sessions are disabled, got: %v", err)
	}
}

// TestSessionInfoToolDisabledWithPool covers the case where a pool exists but
// named sessions are turned off (SessionTTL=0) — Sessions() returns nil.
func TestSessionInfoToolDisabledWithPool(t *testing.T) {
	pool, err := browser.New(browser.Config{
		Logger:     zerolog.Nop(),
		MaxTabs:    1,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 0, // sessions disabled
	})
	if err != nil {
		t.Fatalf("browser.New: %v", err)
	}
	defer pool.Close()

	if pool.Sessions() != nil {
		t.Fatal("expected Sessions() to be nil when SessionTTL=0")
	}

	tool := NewSessionInfoTool(pool)
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"session_id": "anything",
	})
	if err == nil {
		t.Fatal("expected error when named sessions disabled, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention sessions are disabled, got: %v", err)
	}
}

// TestSessionInfoToolListEmpty verifies the list-sessions path (no session_id)
// returns a valid empty list when sessions are enabled but none exist.
func TestSessionInfoToolListEmpty(t *testing.T) {
	pool, err := browser.New(browser.Config{
		Logger:     zerolog.Nop(),
		MaxTabs:    1,
		Headless:   true,
		NoSandbox:  true,
		DisableGPU: true,
		SessionTTL: 30e9, // 30s — enable sessions; using float to avoid time import
	})
	if err != nil {
		t.Fatalf("browser.New: %v", err)
	}
	defer pool.Close()
	_ = config.BrowserConfig{} // keep import if config becomes needed later

	tool := NewSessionInfoTool(pool)
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("list path should not error on empty session set: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result["_metadata"]; !ok {
		t.Error("result should contain _metadata")
	}
}

// TestSessionInfoToolNameAndSchema sanity-checks the tool registration: the
// name, description and schema are what the MCP client and OpenAPI layer see.
func TestSessionInfoToolNameAndSchema(t *testing.T) {
	tool := NewSessionInfoTool(nil)
	if tool.Name() != "session_info" {
		t.Errorf("Name = %q, want session_info", tool.Name())
	}
	schema := tool.InputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("InputSchema has no properties map")
	}
	for _, field := range []string{"session_id", "include_values"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing property %q", field)
		}
	}
}
