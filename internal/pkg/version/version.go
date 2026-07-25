// Package version holds build-time metadata injected via -ldflags.
//
// The values are populated at link time by the build system (Makefile / Dockerfile):
//
//	go build -ldflags "\
//	  -X github.com/metall/mcp-web-scrape/internal/pkg/version.Version=1.0.0 \
//	  -X github.com/metall/mcp-web-scrape/internal/pkg/version.GitCommit=$(git rev-parse --short HEAD) \
//	  -X github.com/metall/mcp-web-scrape/internal/pkg/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// When the binary is built without ldflags (e.g. `go build ./cmd/server` during
// development), the defaults below apply — a visible "dev" marker instead of an
// empty string, so it is obvious the binary was not stamped.
//
// These three variables are the single source of truth for the application
// version: the --version / --help CLI flags, the GET / info endpoint, the MCP
// serverInfo, and the OpenAPI spec all read from here rather than carrying their
// own hard-coded copies (which had drifted to bare "1.0.0" with no commit).
package version

import "fmt"

// Version is the semantic version. Overridden at link time via -ldflags;
// defaults to "dev" for un-stamped local builds.
var Version = "dev"

// GitCommit is the short git SHA the binary was built from. Overridden at link
// time; empty for un-stamped local builds.
var GitCommit = "unknown"

// BuildDate is the UTC build timestamp (RFC3339). Overridden at link time;
// empty for un-stamped local builds.
var BuildDate = "unknown"

// Full returns a single human-readable version string suitable for one-line
// logging and CLI output, e.g. "1.2.3 (abc1234) built 2026-07-25T17:30:00Z".
// When fields are at their defaults it still renders sensibly
// ("dev (unknown) built unknown") rather than a confusing partial string.
func Full() string {
	return fmt.Sprintf("%s (%s) built %s", Version, GitCommit, BuildDate)
}
