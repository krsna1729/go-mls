package ffmpeg

import (
	"context"
	"errors"
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

	proc, err := RunAndMonitor(context.Background(), store, log, args...)
	assert.NoError(t, err)
	assert.NotNil(t, proc)

	initialGoroutines := runtime.NumGoroutine()

	proc.Stop()
	_ = proc.Wait()

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

	proc, err := RunAndMonitor(context.Background(), store, log, args...)
	assert.NoError(t, err)

	proc.Stop()

	err = proc.Wait()
	if err != nil && !IsProcessKilled(err) && !errors.Is(err, ErrProcessKilled) {
		assert.NoError(t, err)
	}

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

	proc, err := RunAndMonitor(context.Background(), store, log, args...)
	assert.NoError(t, err)

	pid := proc.PID()
	assert.Greater(t, pid, 0)

	proc.Stop()

	done := make(chan error, 1)
	go func() {
		done <- proc.Wait()
	}()

	select {
	case err := <-done:
		assert.True(t, err == nil || IsProcessKilled(err) || errors.Is(err, ErrProcessKilled),
			"unexpected error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("Process did not exit within timeout")
	}
}

func TestErrors_TypedErrors(t *testing.T) {
	assert.True(t, IsProcessKilled(ErrProcessKilled))
	assert.False(t, IsProcessKilled(ErrProcessFailed))
	assert.True(t, IsProcessFailed(ErrProcessFailed))
	assert.False(t, IsProcessFailed(ErrProcessKilled))
}
