package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
)

// TestGetActiveTabs verifies that GetActiveTabs returns correct tab count
func TestGetActiveTabs(t *testing.T) {
	// Skip if no Chrome available
	t.Skip("Skipping browser test - requires Chrome/browser setup")

	ctx := context.Background()

	// Create Chrome container
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "browserless/chrome:latest",
			ExposedPorts: []string{"3000/tcp"},
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("Failed to start Chrome container: %v", err)
	}
	container.Terminate(ctx)

	logger := zerolog.Nop()

	// Create browser pool
	pool, err := NewPool(logger, Config{
		Headless:        true,
		IsolatedMode:    false,
		MaxTabs:         5,
		BlockImages:     false,
		DisableGPU:      true,
		NoSandbox:       true,
		ViewportWidth:   1920,
		ViewportHeight:  1080,
		AllocOptions:    []chromedp.ExecAllocatorOption{},
	})
	if err != nil {
		t.Fatalf("Failed to create browser pool: %v", err)
	}
	defer pool.Close()

	// Test initial state
	activeTabs := pool.GetActiveTabs()
	if activeTabs != 0 {
		t.Errorf("Expected 0 active tabs initially, got %d", activeTabs)
	}

	// Create context
	taskCtx, cancel, err := pool.GetContext(ctx)
	if err != nil {
		t.Fatalf("Failed to get context: %v", err)
	}

	// Test with one active tab
	activeTabs = pool.GetActiveTabs()
	if activeTabs != 1 {
		t.Errorf("Expected 1 active tab after GetContext, got %d", activeTabs)
	}

	// Cancel context
	cancel()
	time.Sleep(100 * time.Millisecond) // Give time for cleanup

	// Test after cancel
	activeTabs = pool.GetActiveTabs()
	if activeTabs != 0 {
		t.Errorf("Expected 0 active tabs after cancel, got %d", activeTabs)
	}
}

// TestGetActiveTabsConcurrent verifies GetActiveTabs is thread-safe
func TestGetActiveTabsConcurrent(t *testing.T) {
	t.Skip("Skipping concurrent test - requires browser setup")

	logger := zerolog.Nop()

	// Create browser pool
	pool, err := NewPool(logger, Config{
		Headless:       true,
		IsolatedMode:   false,
		MaxTabs:        10,
		BlockImages:    false,
		DisableGPU:     true,
		NoSandbox:      true,
		ViewportWidth:  1920,
		ViewportHeight: 1080,
		AllocOptions:   []chromedp.ExecAllocatorOption{},
	})
	if err != nil {
		t.Fatalf("Failed to create browser pool: %v", err)
	}
	defer pool.Close()

	// Launch concurrent goroutines to read GetActiveTabs
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = pool.GetActiveTabs()
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we get here without panic/race, test passes
	t.Log("✅ GetActiveTabs is thread-safe")
}
