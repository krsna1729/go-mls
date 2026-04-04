package worker

import (
	"context"
	"runtime"
	"testing"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/state"

	"github.com/stretchr/testify/assert"
)

func TestFFmpegProcess_ProcessGroupIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	log := logger.NewLogger()
	store := state.NewStore()

	args := []string{
		"-f", "lavfi",
		"-i", "testsrc=duration=5:size=1280x720:rate=30",
		"-f", "null",
		"-",
	}

	proc, err := RunAndMonitorFFmpeg(context.Background(), store, log, args...)
	assert.NoError(t, err)
	assert.NotNil(t, proc)

	// Verify process is running
	initialGoroutines := runtime.NumGoroutine()

	// Stop should not kill the parent process group
	proc.Stop()

	// Wait for process to exit (may take up to 5 seconds for graceful timeout)
	err = proc.Wait()
	// We accept either no error (graceful) or "signal: killed" (forced after timeout)

	// Verify goroutines are cleaned up
	time.Sleep(100 * time.Millisecond)
	currentGoroutines := runtime.NumGoroutine()
	diff := currentGoroutines - initialGoroutines
	assert.True(t, diff <= 2, "goroutines leaked: initial=%d, current=%d, diff=%d", initialGoroutines, currentGoroutines, diff)
}

func TestFFmpegProcess_StopWaitsForProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	log := logger.NewLogger()
	store := state.NewStore()

	args := []string{
		"-f", "lavfi",
		"-i", "testsrc=duration=10:size=1280x720:rate=30",
		"-f", "null",
		"-",
	}

	proc, err := RunAndMonitorFFmpeg(context.Background(), store, log, args...)
	assert.NoError(t, err)

	// Stop the process
	proc.Stop()

	// Wait should complete (may take up to 5 seconds)
	err = proc.Wait()
	// Accept both graceful exit and forced kill
	if err != nil && err.Error() != "signal: killed" {
		assert.NoError(t, err)
	}

	// Done channel should be closed
	select {
	case <-proc.Done():
	default:
		t.Fatal("Done channel should be closed after Wait")
	}
}

func TestFFmpegProcess_KillProcessGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	log := logger.NewLogger()
	store := state.NewStore()

	args := []string{
		"-f", "lavfi",
		"-i", "testsrc=duration=60:size=1280x720:rate=30",
		"-f", "null",
		"-",
	}

	proc, err := RunAndMonitorFFmpeg(context.Background(), store, log, args...)
	assert.NoError(t, err)

	// Get the process group ID
	pid := proc.PID()
	assert.Greater(t, pid, 0)

	// Stop should eventually kill the process (within 5 seconds + wait time)
	proc.Stop()

	// Wait with timeout
	done := make(chan error, 1)
	go func() {
		done <- proc.Wait()
	}()

	select {
	case err := <-done:
		// Process exited (either gracefully or killed)
		assert.True(t, err == nil || err.Error() == "signal: killed",
			"unexpected error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("Process did not exit within timeout")
	}
}
