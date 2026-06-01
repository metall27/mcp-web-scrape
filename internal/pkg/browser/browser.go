package browser

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

type Pool struct {
	allocator context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	logger    zerolog.Logger

	// Statistics
	activeTabs int32
	totalTabs  int64

	// Configuration
	maxTabs      int
	allocOptions []chromedp.ExecAllocatorOption
}

type Config struct {
	Logger        zerolog.Logger
	MaxTabs       int  // Maximum concurrent tabs (0 = unlimited)
	Headless      bool
	DisableGPU    bool
	NoSandbox     bool
	ViewportWidth int
	ViewportHeight int
}

func New(cfg Config) (*Pool, error) {
	if cfg.MaxTabs == 0 {
		cfg.MaxTabs = 10 // default
	}

	// Build allocator options
	allocOpts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("site-per-process", false), // Disable site-per-process to prevent iframe issues
		chromedp.Flag("disable-features", "SitePerProcess"), // Disable out-of-process iframes
		chromedp.WindowSize(cfg.ViewportWidth, cfg.ViewportHeight),
	}

	if cfg.Headless {
		allocOpts = append(allocOpts, chromedp.Headless)
	}

	if cfg.DisableGPU {
		allocOpts = append(allocOpts, chromedp.DisableGPU)
	}

	if cfg.NoSandbox {
		allocOpts = append(allocOpts, chromedp.NoSandbox)
	}

	// Create allocator
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)

	pool := &Pool{
		allocator:    allocCtx,
		cancel:       allocCancel,
		logger:       cfg.Logger,
		maxTabs:      cfg.MaxTabs,
		allocOptions: allocOpts,
	}

	cfg.Logger.Info().
		Int("max_tabs", cfg.MaxTabs).
		Bool("headless", cfg.Headless).
		Msg("Browser pool initialized")

	return pool, nil
}

// GetContext creates a new browser context from the pool
func (p *Pool) GetContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	// Check if we've hit the tab limit
	if p.maxTabs > 0 {
		active := atomic.LoadInt32(&p.activeTabs)
		if active >= int32(p.maxTabs) {
			p.logger.Warn().
				Int32("active", active).
				Int("max", p.maxTabs).
				Msg("Max tabs limit reached, waiting...")

			// Wait with timeout
			ctx, cancel := context.WithTimeout(parent, 30*time.Second)
			defer cancel()

			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return nil, nil, fmt.Errorf("timeout waiting for available browser tab")
				case <-ticker.C:
					if atomic.LoadInt32(&p.activeTabs) < int32(p.maxTabs) {
						goto CreateTab
					}
				}
			}
		}
	}

CreateTab:
	// Create new context from allocator with custom error handler
	taskCtx, cancel := chromedp.NewContext(p.allocator,
		chromedp.WithErrorf(func(format string, v ...interface{}) {
			// Filter out noisy chromedp errors for unhandled events
			msg := fmt.Sprintf(format, v...)
			if !shouldLogChromedpError(msg) {
				return
			}
			p.logger.Error().Str("source", "chromedp").Msg(fmt.Sprintf(format, v...))
		}),
		chromedp.WithLogf(func(format string, v ...interface{}) {
			// Filter debug messages
			msg := fmt.Sprintf(format, v...)
			if !shouldLogChromedpError(msg) {
				return
			}
			p.logger.Debug().Str("source", "chromedp").Msg(fmt.Sprintf(format, v...))
		}),
	)

	atomic.AddInt32(&p.activeTabs, 1)
	atomic.AddInt64(&p.totalTabs, 1)

	p.logger.Debug().
		Int32("active", atomic.LoadInt32(&p.activeTabs)).
		Int64("total", atomic.LoadInt64(&p.totalTabs)).
		Msg("Browser context created")

	// Wrap cancel func to decrement counter
	wrapCancel := func() {
		cancel()
		atomic.AddInt32(&p.activeTabs, -1)
		p.logger.Debug().
			Int32("active", atomic.LoadInt32(&p.activeTabs)).
			Msg("Browser context released")
	}

	return taskCtx, wrapCancel, nil
}

// GetStats returns pool statistics
func (p *Pool) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"active_tabs": atomic.LoadInt32(&p.activeTabs),
		"total_tabs":  atomic.LoadInt64(&p.totalTabs),
		"max_tabs":    p.maxTabs,
	}
}

// Close shuts down the browser pool
func (p *Pool) Close() error {
	p.logger.Info().
		Int32("active", atomic.LoadInt32(&p.activeTabs)).
		Msg("Closing browser pool")

	// Cancel allocator - this will close all browser contexts
	p.cancel()

	return nil
}

// shouldLogChromedpError determines if a chromedp error should be logged
func shouldLogChromedpError(msg string) bool {
	// Filter out common chromedp "unhandled event" errors that are not critical
	// These events occur normally during page navigation but don't affect scraping
	ignoredErrors := []string{
		"unhandled page event *page.EventFrameSubtreeWillBeDetached",
		"unhandled page event *page.", // Catch-all for other unhandled page events
	}

	for _, ignored := range ignoredErrors {
		if strings.Contains(msg, ignored) {
			return false
		}
	}

	return true
}
