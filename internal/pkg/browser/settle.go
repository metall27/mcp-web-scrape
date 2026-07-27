package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

// SettleConfig controls the smart-settle wait that runs after mutating actions
// (click/submit/type/navigate/...) when smartSettle is enabled. The goal is to
// behave like a real browser/user: don't snapshot the page the instant an
// async handler fires — wait for the SPA to finish reacting.
//
// Two phases:
//
//  1. Emergence — wait until document.body has non-trivial content. After a
//     navigate action a SPA serves an empty <body></body> shell and only
//     renders after hydration + data fetch. Snapshots taken in this window are
//     empty (the root cause of "Bug B" in issue #71: size_bytes=0 after
//     navigate on my.rebrainme.com).
//
//  2. Stabilize — wait until the body content hash stops changing for
//     stabilityWindow. SPAs keep mutating as data loads (skeleton → content,
//     dashboards filling in panels). A stable hash means the page has settled.
//
// Both phases share a single overall cap (MaxWait) so settle can never hang a
// scrape. The wait is best-effort: a timeout is NOT an error, it just means
// "give up waiting and snapshot whatever we have".
type SettleConfig struct {
	// MaxWait is the overall cap for the whole settle (both phases). Default 5s.
	MaxWait time.Duration
	// MinContentLen is the body.innerText length that marks "emerged from empty".
	// Default 50 chars — small enough for minimal pages, large enough to reject
	// a bare shell/empty-body state.
	MinContentLen int
	// StabilityWindow is how long the body hash must be unchanged before we
	// consider the page stable. Default 1s.
	StabilityWindow time.Duration
	// PollInterval is the probe cadence. Default 250ms.
	PollInterval time.Duration
}

// DefaultSettleConfig returns sensible defaults: 5s cap, 50-char emergence
// threshold, 1s stability window, 250ms poll.
func DefaultSettleConfig() SettleConfig {
	return SettleConfig{
		MaxWait:         5 * time.Second,
		MinContentLen:   50,
		StabilityWindow: 1 * time.Second,
		PollInterval:    250 * time.Millisecond,
	}
}

// settleProbe is the in-page probe: a cheap djb2 hash of a trimmed body
// innerText plus its length. Trimmed to keep the CDP round-trip small.
const settleProbeJS = `(() => {
	const t = (document.body && document.body.innerText) ? document.body.innerText : '';
	const trimmed = t.replace(/\s+/g, ' ').trim().slice(0, 5000);
	let h = 5381;
	for (let i = 0; i < trimmed.length; i++) {
		h = ((h << 5) + h + trimmed.charCodeAt(i)) | 0;
	}
	return { hash: String(h), len: t.replace(/\s+/g,' ').trim().length };
})()`

type settleProbeResult struct {
	Hash string `json:"hash"`
	Len  int    `json:"len"`
}

// SmartSettle waits for the page to settle after a mutating action. It is a
// best-effort, non-fatal wait: a timeout returns nil (the caller snapshots
// whatever state was reached). logger may be nil (logging is skipped).
//
// The returned SettleReport summarises what happened for observability
// (surfaced in metadata so the LLM can see how long the page took to settle).
func SmartSettle(ctx context.Context, logger zerolog.Logger, cfg SettleConfig) (*SettleReport, error) {
	if cfg.MaxWait <= 0 {
		cfg = DefaultSettleConfig()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.StabilityWindow <= 0 {
		cfg.StabilityWindow = 1 * time.Second
	}
	if cfg.MinContentLen <= 0 {
		cfg.MinContentLen = 50
	}

	deadline := time.Now().Add(cfg.MaxWait)
	report := &SettleReport{}

	// Phase 1: emergence — wait for non-trivial body content.
	emergenceStart := time.Now()
	var last settleProbeResult
	for {
		if err := ctx.Err(); err != nil {
			report.Phase = "canceled"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			return report, err
		}
		if time.Now().After(deadline) {
			// Never emerged (empty body the whole time). Snapshot whatever we
			// have — this is the post-navigate empty-shell case; better to
			// return and let observation/log show it than to hang.
			report.Phase = "emergence_timeout"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			if logger.GetLevel() <= zerolog.DebugLevel {
				logger.Debug().
					Int64("total_ms", report.TotalMs).
					Msg("settle: body never reached min content length")
			}
			return report, nil
		}
		if err := chromedp.Evaluate(settleProbeJS, &last).Do(ctx); err != nil {
			report.Phase = "probe_error"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			return report, fmt.Errorf("settle probe: %w", err)
		}
		if last.Len >= cfg.MinContentLen {
			break
		}
		sleepCtx(ctx, cfg.PollInterval)
	}
	report.EmergedMs = time.Since(emergenceStart).Milliseconds()

	// Phase 2: stabilize — wait for the body hash to stop changing.
	stableSince := time.Now()
	prev := last
	for {
		if err := ctx.Err(); err != nil {
			report.Phase = "canceled"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			return report, err
		}
		if time.Now().After(deadline) {
			report.Phase = "stabilize_timeout"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			if logger.GetLevel() <= zerolog.DebugLevel {
				logger.Debug().
					Int64("total_ms", report.TotalMs).
					Int64("emerged_ms", report.EmergedMs).
					Msg("settle: page still changing at cap, snapshotting current state")
			}
			return report, nil
		}
		sleepCtx(ctx, cfg.PollInterval)
		if err := chromedp.Evaluate(settleProbeJS, &last).Do(ctx); err != nil {
			report.Phase = "probe_error"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			return report, fmt.Errorf("settle probe: %w", err)
		}
		if last.Hash != prev.Hash {
			// Content changed — reset the stability clock.
			stableSince = time.Now()
			prev = last
			continue
		}
		if time.Since(stableSince) >= cfg.StabilityWindow {
			report.Phase = "stable"
			report.TotalMs = time.Since(emergenceStart).Milliseconds()
			if logger.GetLevel() <= zerolog.DebugLevel {
				logger.Debug().
					Int64("total_ms", report.TotalMs).
					Int64("emerged_ms", report.EmergedMs).
					Int("content_len", last.Len).
					Msg("settle: page stable")
			}
			return report, nil
		}
	}
}

// SettleReport summarises a SmartSettle run. Surfaced in per-action metadata so
// the LLM can see whether the page actually settled (and how long it took)
// rather than guessing from a stale snapshot.
type SettleReport struct {
	Phase     string `json:"phase"`      // "stable" | "emergence_timeout" | "stabilize_timeout" | "canceled" | "probe_error"
	EmergedMs int64  `json:"emerged_ms"` // time to reach min content length (phase 1)
	TotalMs   int64  `json:"total_ms"`   // total settle time
}

// sleepCtx sleeps for d but returns early if ctx is canceled.
func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
