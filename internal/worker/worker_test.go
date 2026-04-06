package worker

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"go-mls/internal/ffmpeg"
	"go-mls/internal/logger"

	"github.com/stretchr/testify/assert"
)

func TestWorkerState_String(t *testing.T) {
	tests := []struct {
		state    WorkerState
		expected string
	}{
		{WorkerStateStopped, "stopped"},
		{WorkerStateStarting, "starting"},
		{WorkerStateRunning, "running"},
		{WorkerStateStopping, "stopping"},
		{WorkerStateError, "error"},
		{WorkerState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestNewBaseWorker(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	assert.Equal(t, "test", w.name)
	assert.Equal(t, WorkerStateStopped, w.State())
	assert.NotNil(t, w.stopCh)
	assert.NotNil(t, w.doneCh)
}

func TestBaseWorker_Start(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	err := w.Start()
	assert.NoError(t, err)
	assert.Equal(t, WorkerStateStarting, w.State())

	err = w.Start()
	assert.Error(t, err)
}

func TestBaseWorker_Stop(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.Start()
	w.Stop()

	select {
	case _, ok := <-w.stopCh:
		assert.True(t, ok, "stopCh should be open and receive a value")
	case <-time.After(time.Second):
		t.Fatal("stopCh should receive a signal")
	}
}

func TestBaseWorker_Wait(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.Start()
	w.complete(nil)

	err := w.Wait()
	assert.NoError(t, err)
	assert.Equal(t, WorkerStateStopped, w.State())
}

func TestBaseWorker_CompleteWithError(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.Start()
	w.complete(assert.AnError)

	err := w.Wait()
	assert.Error(t, err)
	assert.Equal(t, WorkerStateError, w.State())
}

func TestBaseWorker_Done(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.Start()
	w.complete(nil)

	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("doneCh should be closed")
	}
}

func TestProcessWorker(t *testing.T) {
	log := logger.NewLogger()
	w := NewProcessWorker("test", log)

	assert.NotNil(t, w.BaseWorker)
	assert.Equal(t, "test", w.name)
	assert.Equal(t, WorkerStateStopped, w.State())
}

func TestProcessWorker_WithProcess(t *testing.T) {
	log := logger.NewLogger()
	w := NewProcessWorker("test", log)

	proc := &mockProcess{}
	w.WithProcess(proc)

	assert.Equal(t, proc, w.proc)
}

func TestProcessWorker_Stop(t *testing.T) {
	log := logger.NewLogger()
	w := NewProcessWorker("test", log)

	proc := &mockProcess{}
	w.WithProcess(proc)

	w.Stop()
	assert.True(t, proc.stopped)
}

type mockProcess struct {
	stopped bool
}

func (m *mockProcess) PID() int    { return 0 }
func (m *mockProcess) Stop()       { m.stopped = true }
func (m *mockProcess) Wait() error { return nil }
func (m *mockProcess) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (m *mockProcess) Err() error { return nil }

type mockProcessWithDone struct {
	blocked  chan struct{}
	stopOnce sync.Once
}

func (m *mockProcessWithDone) PID() int { return 0 }
func (m *mockProcessWithDone) Stop() {
	// Stop is expected to be idempotent; the process owner may call it more
	// than once (e.g. via ProcessWorker.Stop() and startProcessLoop).
	m.stopOnce.Do(func() { close(m.blocked) })
}
func (m *mockProcessWithDone) Wait() error {
	<-m.blocked
	return nil
}
func (m *mockProcessWithDone) Done() <-chan struct{} { return m.blocked }
func (m *mockProcessWithDone) Err() error            { return nil }

func TestNoopFFmpegProcess(t *testing.T) {
	p := &ffmpeg.NoopFFmpegProcess{}

	assert.Equal(t, 0, p.PID())
	assert.NotPanics(t, func() { p.Stop() })
	assert.NotPanics(t, func() { p.Wait() })
	assert.NotPanics(t, func() { p.Err() })

	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("Done should be closed")
	}
}

func TestProcessInterface(t *testing.T) {
	var _ Process = (*ffmpeg.NoopFFmpegProcess)(nil)
	var _ Process = (*mockProcess)(nil)
}

func TestRunProcessWorker(t *testing.T) {
	log := logger.NewLogger()

	w, err := RunProcessWorker(context.Background(), "test", log, func(ctx context.Context) (Process, error) {
		return nil, nil
	})

	assert.NoError(t, err)
	assert.NotNil(t, w)
}

func TestRunProcessWorkerWithError(t *testing.T) {
	log := logger.NewLogger()
	customErr := fmt.Errorf("factory error")

	w, err := RunProcessWorker(context.Background(), "test", log, func(ctx context.Context) (Process, error) {
		return nil, customErr
	})

	assert.NoError(t, err)
	assert.NotNil(t, w)

	time.Sleep(100 * time.Millisecond)
	err = w.Wait()
	assert.Error(t, err, "Wait should return the error from factory")
	assert.Equal(t, customErr, err)
}

func TestRunProcessWorker_GoroutineCleanup(t *testing.T) {
	log := logger.NewLogger()

	started := make(chan struct{})
	done := make(chan struct{})

	w, err := RunProcessWorker(context.Background(), "test-cleanup", log, func(ctx context.Context) (Process, error) {
		proc := &mockProcessWithDone{blocked: make(chan struct{})}
		close(started)
		<-done // Block until we signal
		return proc, nil
	})

	assert.NoError(t, err)
	<-started // Wait for factory to be called

	// Record goroutine count before stop
	initialGoroutines := runtime.NumGoroutine()

	// Stop the worker
	w.Stop()

	// Signal the blocked process to exit
	close(done)

	// Wait for worker to complete
	err = w.Wait()
	assert.NoError(t, err)

	// Give a small time window for cleanup
	time.Sleep(100 * time.Millisecond)

	// Verify goroutine count is back to initial (or close to it)
	currentGoroutines := runtime.NumGoroutine()
	diff := currentGoroutines - initialGoroutines
	assert.True(t, diff <= 1, "goroutines leaked: initial=%d, current=%d, diff=%d", initialGoroutines, currentGoroutines, diff)
}

func TestRunProcessWorker_StopBeforeProcessStarts(t *testing.T) {
	log := logger.NewLogger()

	block := make(chan struct{})
	unblock := make(chan struct{})

	w, err := RunProcessWorker(context.Background(), "test-stop-before", log, func(ctx context.Context) (Process, error) {
		<-block // Block until we signal
		proc := &mockProcessWithDone{blocked: make(chan struct{})}
		select {
		case <-unblock:
			return proc, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	assert.NoError(t, err)

	// Stop before the factory has returned
	w.Stop()
	close(block)

	// Signal the unblock
	close(unblock)

	// Wait for worker to complete
	err = w.Wait()
	assert.NoError(t, err)

	// Verify state transitions to stopped (not error)
	assert.Equal(t, WorkerStateStopped, w.State())
}

func TestProcessWorker_Shutdown(t *testing.T) {
	log := logger.NewLogger()

	w, err := RunProcessWorker(context.Background(), "test-shutdown", log, func(ctx context.Context) (Process, error) {
		proc := &mockProcessWithDone{blocked: make(chan struct{})}
		return proc, nil
	})

	assert.NoError(t, err)

	// Call Shutdown
	w.Shutdown()

	// Verify state is stopped
	assert.Equal(t, WorkerStateStopped, w.State())
}

// TestProcessWorker_ConcurrentStop fires Stop() from many goroutines at once.
// procMu protects w.proc, so the race detector must stay silent.
func TestProcessWorker_ConcurrentStop(t *testing.T) {
	log := logger.NewLogger()

	blocked := make(chan struct{})
	w, err := RunProcessWorker(context.Background(), "concurrent-stop", log, func(ctx context.Context) (Process, error) {
		return &mockProcessWithDone{blocked: blocked}, nil
	})
	assert.NoError(t, err)

	// Give the factory goroutine time to assign the process.
	time.Sleep(50 * time.Millisecond)

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w.Stop()
		}()
	}
	wg.Wait()

	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish after concurrent Stop() calls")
	}
}

// TestProcessWorker_ConcurrentStopAndWait exercises concurrent Stop() and
// Wait() calls to ensure no goroutine blocks indefinitely on a closed channel.
func TestProcessWorker_ConcurrentStopAndWait(t *testing.T) {
	log := logger.NewLogger()

	blocked := make(chan struct{})
	w, err := RunProcessWorker(context.Background(), "stop-and-wait", log, func(ctx context.Context) (Process, error) {
		return &mockProcessWithDone{blocked: blocked}, nil
	})
	assert.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w.Stop()
		}()
		go func() {
			defer wg.Done()
			// Wait must return, not hang.
			done := make(chan struct{})
			go func() {
				_ = w.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Errorf("Wait() blocked indefinitely")
			}
		}()
	}
	wg.Wait()
}

// TestProcessWorker_WithProcessConcurrent verifies that WithProcess() is
// safe to call from one goroutine while Stop() is called from another.
func TestProcessWorker_WithProcessConcurrent(t *testing.T) {
	log := logger.NewLogger()
	w := NewProcessWorker("concurrent-with-proc", log)

	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				w.WithProcess(&mockProcess{})
			}()
		} else {
			go func() {
				defer wg.Done()
				w.Stop()
			}()
		}
	}
	wg.Wait()
}

// TestBaseWorker_ConcurrentStop ensures that concurrent BaseWorker.Stop()
// calls never panic or deadlock on the buffered stopCh.
func TestBaseWorker_ConcurrentStop(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("concurrent-base-stop", log)
	_ = w.Start()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w.Stop()
		}()
	}
	wg.Wait()

	// Drain the stop channel so we can call complete.
	select {
	case <-w.stopCh:
	default:
	}
	w.complete(nil)
	assert.Equal(t, WorkerStateStopped, w.State())
}

// TestProcessWorker_GoroutineLeak verifies that the internal goroutine
// launched by RunProcessWorker exits after the worker completes.
func TestProcessWorker_GoroutineLeak(t *testing.T) {
	log := logger.NewLogger()

	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	blocked := make(chan struct{})
	w, err := RunProcessWorker(context.Background(), "leak-check", log, func(ctx context.Context) (Process, error) {
		return &mockProcessWithDone{blocked: blocked}, nil
	})
	assert.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	w.Stop()

	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish")
	}

	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after-baseline, 2,
		"goroutine leak: baseline=%d after=%d", baseline, after)
}

