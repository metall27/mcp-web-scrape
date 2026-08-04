package main

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestReapZombiesDrainsAndExits verifies the SIGCHLD reaper:
//   - it reaps an exited child without blocking, and
//   - crucially, it does NOT busy-spin while a long-lived child is still
//     alive. The original drain loop only broke on err != nil and spun
//     forever on the pid==0 / err==nil return (a child exists but hasn't
//     exited), which pinned a CPU core for the server's lifetime because
//     the browser pool keeps a Chrome process resident (#93).
//
// The regression is detected by closing `done` after a SIGCHLD: with the
// buggy loop the goroutine never returns to the select (it's stuck in the
// inner for), so the shutdown times out. With the fix the inner loop
// breaks on pid==0, control returns to the select, and `done` unblocks it.
func TestReapZombiesDrainsAndExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGCHLD reaper is Unix-only")
	}

	// Long-lived child — simulates the resident Chrome process that makes
	// Wait4 keep returning pid==0. Kept alive for the whole test.
	long := exec.Command("sleep", "30")
	if err := long.Start(); err != nil {
		t.Fatalf("start long child: %v", err)
	}
	defer func() { _ = long.Process.Kill() }()

	// Short child that exits immediately and becomes a zombie for the
	// reaper to collect. We deliberately do NOT call short.Wait() — that
	// would reap it via exec.Cmd and steal it from Wait4(-1).
	short := exec.Command("true")
	if err := short.Start(); err != nil {
		t.Fatalf("start short child: %v", err)
	}
	// Give it time to exit and turn into a zombie.
	time.Sleep(50 * time.Millisecond)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGCHLD)
	defer signal.Stop(sigCh)

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		reapZombies(sigCh, done)
		close(finished)
	}()

	// Deliver a SIGCHLD. The reaper enters its drain loop: it reaps the
	// short zombie (pid>0) then hits the long child still alive (pid==0).
	// The fixed loop breaks here; the buggy loop spins forever.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGCHLD); err != nil {
		t.Fatalf("send SIGCHLD: %v", err)
	}

	// Let the reaper run its drain, then request shutdown.
	time.Sleep(100 * time.Millisecond)
	close(done)

	select {
	case <-finished:
		// reaper returned to its select and shut down — good.
	case <-time.After(2 * time.Second):
		t.Fatal("reapZombies did not shut down within 2s — drain loop is busy-spinning on pid==0")
	}

	// Sanity: the long child must still be alive (the reaper must not have
	// killed or stolen it). A zero-signal probe succeeds while the proc lives.
	if err := long.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("long-lived child disappeared after reaper ran: %v", err)
	}
}
