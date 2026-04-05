package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go-mls/internal/logger"
)

type WorkerState int

const (
	WorkerStateStopped WorkerState = iota
	WorkerStateStarting
	WorkerStateRunning
	WorkerStateStopping
	WorkerStateError
)

func (s WorkerState) String() string {
	switch s {
	case WorkerStateStopped:
		return "stopped"
	case WorkerStateStarting:
		return "starting"
	case WorkerStateRunning:
		return "running"
	case WorkerStateStopping:
		return "stopping"
	case WorkerStateError:
		return "error"
	default:
		return "unknown"
	}
}

type Worker interface {
	Start(ctx context.Context) error
	Stop()
	Wait() error
	State() WorkerState
	Done() <-chan struct{}
}

type Process interface {
	PID() int
	Stop()
	Wait() error
	Done() <-chan struct{}
	Err() error
}

type BaseWorker struct {
	name string
	log  *logger.Logger

	mu          sync.RWMutex
	state       WorkerState
	stopCh      chan struct{}
	doneCh      chan struct{}
	exitErr     error
	goroutineWG sync.WaitGroup
}

func NewBaseWorker(name string, log *logger.Logger) *BaseWorker {
	return &BaseWorker{
		name:   name,
		log:    log.With("worker", name),
		state:  WorkerStateStopped,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (w *BaseWorker) State() WorkerState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

func (w *BaseWorker) Done() <-chan struct{} {
	return w.doneCh
}

func (w *BaseWorker) Wait() error {
	<-w.doneCh
	return w.exitErr
}

func (w *BaseWorker) Stop() {
	select {
	case w.stopCh <- struct{}{}:
		w.log.Debug("Stop signal sent", "worker", w.name)
	default:
		w.log.Debug("Stop already requested", "worker", w.name)
	}
}

func (w *BaseWorker) StopAndWait() {
	w.Stop()
	w.Wait()
}

func (w *BaseWorker) setState(s WorkerState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state = s
	w.log.Debug("State transition", "worker", w.name, "state", s)
}

func (w *BaseWorker) Start() error {
	w.mu.Lock()
	if w.state != WorkerStateStopped {
		w.mu.Unlock()
		return fmt.Errorf("worker %s already started", w.name)
	}
	w.state = WorkerStateStarting
	w.stopCh = make(chan struct{}, 1)
	w.doneCh = make(chan struct{})
	w.mu.Unlock()
	return nil
}

func (w *BaseWorker) complete(err error) {
	w.mu.Lock()
	w.exitErr = err
	if err != nil {
		w.state = WorkerStateError
	} else {
		w.state = WorkerStateStopped
	}
	w.mu.Unlock()
	close(w.doneCh)
}

type ProcessWorker struct {
	*BaseWorker
	procMu sync.Mutex
	proc   Process
}

func NewProcessWorker(name string, log *logger.Logger) *ProcessWorker {
	return &ProcessWorker{
		BaseWorker: NewBaseWorker(name, log),
	}
}

func (w *ProcessWorker) WithProcess(proc Process) *ProcessWorker {
	w.proc = proc
	return w
}

func (w *ProcessWorker) Stop() {
	w.procMu.Lock()
	if w.proc != nil {
		w.proc.Stop()
	}
	w.procMu.Unlock()
	w.BaseWorker.Stop()
}

func (w *ProcessWorker) WaitForProcess(timeout time.Duration) error {
	select {
	case <-w.Done():
	case <-time.After(timeout):
		return fmt.Errorf("process did not exit within %v", timeout)
	}
	return w.exitErr
}

type ProcessFactory func(ctx context.Context) (Process, error)

func RunProcessWorker(name string, log *logger.Logger, factory ProcessFactory) (*ProcessWorker, error) {
	w := NewProcessWorker(name, log)

	if err := w.Start(); err != nil {
		return nil, err
	}

	w.goroutineWG.Add(1)
	go func() {
		defer func() {
			w.goroutineWG.Done()
			w.complete(w.exitErr)
		}()

		proc, err := factory(context.Background())
		if err != nil {
			w.exitErr = err
			w.log.Error("Failed to start process", "error", err)
			return
		}

		if proc == nil {
			return
		}

		w.procMu.Lock()
		w.proc = proc
		w.procMu.Unlock()
		w.setState(WorkerStateRunning)

		select {
		case <-w.stopCh:
			proc.Stop()
			if err := proc.Wait(); err != nil {
				if !errors.Is(err, ErrProcessKilled) && !IsProcessKilled(err) {
					w.exitErr = fmt.Errorf("%w: %v", ErrProcessFailed, err)
					w.log.Error("Process exited with error", "error", err)
				}
			}
		case <-proc.Done():
			if err := proc.Err(); err != nil {
				if IsProcessKilled(err) {
					w.exitErr = fmt.Errorf("%w: %v", ErrProcessKilled, err)
				} else {
					w.exitErr = fmt.Errorf("%w: %v", ErrProcessFailed, err)
				}
				w.log.Error("Process exited with error", "error", err)
			}
		}
	}()

	return w, nil
}

func (w *ProcessWorker) Shutdown() {
	w.Stop()
	w.goroutineWG.Wait()
}

func (w *ProcessWorker) StartWithFactory(factory ProcessFactory) (*ProcessWorker, error) {
	if err := w.Start(); err != nil {
		return nil, err
	}

	w.goroutineWG.Add(1)
	go func() {
		defer func() {
			w.goroutineWG.Done()
			w.complete(w.exitErr)
		}()

		proc, err := factory(context.Background())
		if err != nil {
			w.exitErr = err
			w.log.Error("Failed to start process", "error", err)
			return
		}

		if proc == nil {
			return
		}

		w.procMu.Lock()
		w.proc = proc
		w.procMu.Unlock()
		w.setState(WorkerStateRunning)

		select {
		case <-w.stopCh:
			proc.Stop()
			if err := proc.Wait(); err != nil {
				if !errors.Is(err, ErrProcessKilled) && !IsProcessKilled(err) {
					w.exitErr = fmt.Errorf("%w: %v", ErrProcessFailed, err)
					w.log.Error("Process exited with error", "error", err)
				}
			}
		case <-proc.Done():
			if err := proc.Err(); err != nil {
				if IsProcessKilled(err) {
					w.exitErr = fmt.Errorf("%w: %v", ErrProcessKilled, err)
				} else {
					w.exitErr = fmt.Errorf("%w: %v", ErrProcessFailed, err)
				}
				w.log.Error("Process exited with error", "error", err)
			}
		}
	}()

	return w, nil
}
