package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
)

// SessionInfoTool inspects a named persistent browser session (#42).
//
// Named sessions (created by scrape_with_js with session_id) carry login
// cookies and Web Storage across calls, but their contents were previously
// invisible — the only way to peek was execute_js with document.cookie, which
// cannot see HTTP-only cookies. This tool dumps the session state (including
// HTTP-only cookies via CDP) so a caller debugging a login-gated workflow can
// answer: am I actually logged in? did the login cookie land? did it expire?
type SessionInfoTool struct {
	*BaseTool
	browserPool *browser.Pool
	logger      zerolog.Logger
}

// NewSessionInfoTool creates the session_info tool. It needs the browser pool
// to reach the SessionManager; without it (nil pool / named sessions disabled)
// the tool reports that named sessions are disabled rather than failing.
func NewSessionInfoTool(browserPool *browser.Pool) *SessionInfoTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_id": map[string]interface{}{
				"type":        "string",
				"description": "The named session to inspect. Omit (or leave empty) to list all active sessions without dumping any single one.",
			},
			"include_values": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, include cookie Values and storage Values in the dump. WARNING: these are sensitive credentials (session tokens, etc.) and will enter the tool response — leave false unless you specifically need to read the token value. Default false (metadata + keys only).",
				"default":     false,
			},
		},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		tool := &SessionInfoTool{
			browserPool: browserPool,
			logger:      logger.Get(),
		}
		return tool.execute(ctx, args)
	}

	tool := &SessionInfoTool{
		browserPool: browserPool,
		logger:      logger.Get(),
	}

	tool.BaseTool = NewBaseTool(
		"session_info",
		"Inspect a named persistent browser session (created via scrape_with_js with a session_id) for debugging login-gated workflows. "+
			"Returns the session's cookies (including HTTP-only cookies, which document.cookie cannot see), localStorage/sessionStorage keys, creation time, last access, and pinned User-Agent. "+
			"Use this to verify whether a login actually took: 'is the session cookie present and not expired?' or 'which sessions currently exist?'. "+
			"By default only cookie metadata and storage keys are returned; set include_values=true to also read token values (sensitive — they enter the response).",
		schema,
		handler,
	)

	return tool
}

func (t *SessionInfoTool) execute(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	// Named sessions require a browser pool with a configured TTL. Without it
	// (nil pool, or SessionTTL==0), report clearly rather than crashing.
	if t.browserPool == nil {
		return nil, fmt.Errorf("named sessions are disabled (no browser pool configured); set browser.session_ttl > 0 to enable")
	}
	sm := t.browserPool.Sessions()
	if sm == nil {
		return nil, fmt.Errorf("named sessions are disabled (session_ttl is 0); set browser.session_ttl > 0 to enable")
	}

	sessionID, _ := args["session_id"].(string)
	includeValues, _ := args["include_values"].(bool)

	// No session_id: list all active sessions (no cookie/storage contents).
	// Lets the caller discover which sessions exist before dumping one.
	if sessionID == "" {
		sessions := sm.List()
		t.logger.Info().Int("count", len(sessions)).Msg("Listed named sessions")
		return BuildMCPResponse(map[string]interface{}{
			"sessions":      sessions,
			"session_count": len(sessions),
		}, map[string]interface{}{
			"hint": "Pass a session_id to dump its cookies and storage keys.",
		})
	}

	// Dump a single session. Wrap in a timeout so a stuck Chrome does not hang
	// the whole tool call — 15s is plenty for two CDP round-trips.
	dumpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	dump, ok := sm.Dump(dumpCtx, sessionID, includeValues)
	if !ok {
		return nil, fmt.Errorf("session %q not found; it may have never been created or may have expired (session_ttl). Use session_info with no session_id to list active sessions", sessionID)
	}

	t.logger.Info().
		Str("session_id", sessionID).
		Int("cookie_count", dump.CookieCount).
		Bool("include_values", includeValues).
		Msg("Dumped named session")

	return BuildMCPResponse(dump, map[string]interface{}{
		"session_id":           sessionID,
		"include_values":       includeValues,
		"cookie_count":         dump.CookieCount,
		"local_storage_keys":   len(dump.LocalStorage.Keys),
		"session_storage_keys": len(dump.SessionStorage.Keys),
	})
}
