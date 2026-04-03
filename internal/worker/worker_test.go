package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

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

	err := w.start()
	assert.NoError(t, err)
	assert.Equal(t, WorkerStateStarting, w.State())

	err = w.start()
	assert.Error(t, err)
}

func TestBaseWorker_Stop(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.start()
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

	w.start()
	w.complete(nil)

	err := w.Wait()
	assert.NoError(t, err)
	assert.Equal(t, WorkerStateStopped, w.State())
}

func TestBaseWorker_CompleteWithError(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.start()
	w.complete(assert.AnError)

	err := w.Wait()
	assert.Error(t, err)
	assert.Equal(t, WorkerStateError, w.State())
}

func TestBaseWorker_Done(t *testing.T) {
	log := logger.NewLogger()
	w := NewBaseWorker("test", log)

	w.start()
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

func TestNoopFFmpegProcess(t *testing.T) {
	p := &NoopFFmpegProcess{}

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
	var _ Process = (*NoopFFmpegProcess)(nil)
	var _ Process = (*mockProcess)(nil)
}

func TestRunProcessWorker(t *testing.T) {
	log := logger.NewLogger()

	w, err := RunProcessWorker("test", log, func(ctx context.Context) (Process, error) {
		return nil, nil
	})

	assert.NoError(t, err)
	assert.NotNil(t, w)
}

func TestRunProcessWorkerWithError(t *testing.T) {
	log := logger.NewLogger()
	customErr := fmt.Errorf("factory error")

	w, err := RunProcessWorker("test", log, func(ctx context.Context) (Process, error) {
		return nil, customErr
	})

	assert.NoError(t, err)
	assert.NotNil(t, w)

	time.Sleep(100 * time.Millisecond)
	err = w.Wait()
	assert.Error(t, err, "Wait should return the error from factory")
	assert.Equal(t, customErr, err)
}

func TestProcessCreator(t *testing.T) {
	c := &DefaultProcessCreator{}
	var _ ProcessCreator = c
}
