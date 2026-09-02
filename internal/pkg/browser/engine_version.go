package browser

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Engine-version UA sync (#105, "ua-sync").
//
// The engine shipped in the Docker image drifts ahead of the static UA pool
// (Chromium 149 in the image vs Chrome 120-124 in the rotator). A page can
// detect that with one line — `typeof Promise.try` (Chrome 128+) exists while
// the UA claims 122 — and no stealth JS can hide engine-shipped APIs. The
// only fix is to advertise the version the engine actually is.
//
// EngineChromeMajor lazily launches a throwaway tab in the pool's allocator,
// reads the engine's own user agent (always truthful — it is not subject to
// the Emulation override in a fresh context) and caches the major version for
// the pool's lifetime.

var engineUARe = regexp.MustCompile(`Chrome/(\d+)`)

// engineVersion is cached per Pool.
type engineVersion struct {
	once  sync.Once
	major int    // 0 = detection failed, sync disabled
	rawUA string // e.g. "HeadlessChrome/149.0.7702.0"
	err   error
}

// EngineChromeMajor returns the major Chromium version backing this pool
// (e.g. 149), or 0 if it could not be determined. Result is cached: the
// binary does not change under us.
func (p *Pool) EngineChromeMajor() int {
	p.engineOnce.Do(func() {
		tabCtx, tabCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer tabCancel()
		tabCtx, tabCancel2 := chromedp.NewContext(p.allocator)
		defer tabCancel2()
		var rawUA string
		if err := chromedp.Run(tabCtx,
			chromedp.Navigate("about:blank"),
			chromedp.Evaluate(`navigator.userAgent`, &rawUA),
		); err != nil {
			p.engine.err = err
			p.logger.Debug().Err(err).Msg("engine version detection failed; UA sync disabled")
			return
		}
		p.engine.rawUA = rawUA
		if m := engineUARe.FindStringSubmatch(rawUA); m != nil {
			p.engine.major, _ = strconv.Atoi(m[1])
		}
		p.logger.Debug().
			Str("engine_ua", rawUA).
			Int("engine_major", p.engine.major).
			Msg("engine version detected")
	})
	return p.engine.major
}

// SyncUserAgentToEngine rewrites the Chrome major version in a UA string to
// the engine's actual major version, keeping everything else (platform,
// WebKit tokens, full-version shape) intact. Non-Chrome UAs and detection
// failures are returned unchanged.
//
// This is the ua-sync half of #105: the advertised identity must not claim a
// Chrome older than the APIs the engine actually ships.
func (p *Pool) SyncUserAgentToEngine(ua string) string {
	major := p.EngineChromeMajor()
	if ua == "" || major == 0 {
		return ua
	}
	m := chromeVersionRe.FindStringSubmatchIndex(ua)
	if m == nil {
		// Not a Chrome UA (Firefox/Safari custom UA) — cannot sync, and the
		// chrome-only rotator never picks one anyway.
		return ua
	}
	// m[2]:m[3] is the major-version capture of Chrome/(\d+).
	return ua[:m[2]] + strconv.Itoa(major) + ua[m[3]:]
}
