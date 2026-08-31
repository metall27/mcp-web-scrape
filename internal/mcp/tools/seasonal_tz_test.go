package tools

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestSeasonalZoneNameMechanism (throwaway verification for the #95 review
// fix): proves the Intl-based resolution the stealth script uses produces
// DIFFERENT names for January vs July in DST zones — i.e. the dynamic
// resolution is genuinely seasonal and a hardcoded "Daylight" name would
// contradict the winter offset. Run-time anchored assertions can't show this
// in August (both resolve to the summer name).
func TestSeasonalZoneNameMechanism(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Chrome binary")
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", "new"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, 60*time.Second)
	defer runCancel()

	var probe struct {
		Jan string `json:"jan"`
		Jul string `json:"jul"`
	}
	err := chromedp.Run(runCtx,
		chromedp.Navigate(`data:text/html,<html><body>tz</body></html>`),
		chromedp.Evaluate(`(() => {
			const zoneName = (d) => new Intl.DateTimeFormat('en-US', {
				timeZone: 'America/New_York', timeZoneName: 'long'
			}).formatToParts(d).find(p => p.type === 'timeZoneName').value;
			return {
				jan: zoneName(new Date('2026-01-15T12:00:00Z')),
				jul: zoneName(new Date('2026-07-15T12:00:00Z'))
			};
		})()`, &probe),
	)
	if err != nil {
		t.Fatalf("chromedp run failed: %v", err)
	}

	t.Logf("January name: %q, July name: %q", probe.Jan, probe.Jul)
	if probe.Jan == probe.Jul {
		t.Fatalf("Intl zone names are not seasonal in this Chrome: jan=%q jul=%q — dynamic resolution would be pointless", probe.Jan, probe.Jul)
	}
	if probe.Jan != "Eastern Standard Time" {
		t.Errorf("January name = %q, want 'Eastern Standard Time'", probe.Jan)
	}
	if probe.Jul != "Eastern Daylight Time" {
		t.Errorf("July name = %q, want 'Eastern Daylight Time'", probe.Jul)
	}
}
