// Package shutdown provides a context-driven shutdown manager for coordinating
// graceful application shutdown across multiple components
package shutdown

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Manager coordinates graceful shutdown of application components
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	funcs  []func(context.Context) error
}

// NewManager creates a new shutdown manager
func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		ctx:    ctx,
		cancel: cancel,
		funcs:  make([]func(context.Context) error, 0),
	}
}

// Context returns the shutdown context that will be canceled when shutdown is initiated
func (m *Manager) Context() context.Context {
	return m.ctx
}

// Register registers a function to be called during shutdown
// Functions are called in the order they were registered
// Each function should respect the context and return when it's done or canceled
func (m *Manager) Register(fn func(context.Context) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.funcs = append(m.funcs, fn)
}

// Shutdown initiates graceful shutdown with the given timeout
// It cancels the context and waits for all registered functions to complete
// Returns an error if any function returns an error or if timeout is exceeded
func (m *Manager) Shutdown(timeout time.Duration) error {
	// Cancel the context to signal shutdown to all components
	m.cancel()

	// Create a context with timeout for shutdown operations
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Channel to collect errors from shutdown functions
	errChan := make(chan error, 100)

	// Execute all shutdown functions
	m.mu.Lock()
	funcs := make([]func(context.Context) error, len(m.funcs))
	copy(funcs, m.funcs)
	m.mu.Unlock()

	// Run each function in a goroutine
	for _, fn := range funcs {
		m.wg.Add(1)
		go func(f func(context.Context) error) {
			defer m.wg.Done()
			if err := f(ctx); err != nil {
				select {
				case errChan <- err:
				case <-ctx.Done():
					// Timeout, don't block on error channel
				}
			}
		}(fn)
	}

	// Wait for completion or timeout
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	// Collect any errors
	var errors []error
	errorCollector := make(chan struct{})

	go func() {
		for err := range errChan {
			errors = append(errors, err)
		}
		close(errorCollector)
	}()

	select {
	case <-done:
		// All functions completed
		close(errChan)
		<-errorCollector
	case <-ctx.Done():
		// Timeout exceeded - stop waiting
		errors = append(errors, fmt.Errorf("shutdown timeout exceeded"))
		// Don't wait for all functions to complete, return immediately
		go func() {
			<-done // Wait in background
			close(errChan)
		}()
	}

	if len(errors) > 0 {
		return fmt.Errorf("shutdown completed with %d error(s): %v", len(errors), errors)
	}

	return nil
}

// ShutdownWithFunc is a convenience method that takes a function and immediately
// shuts down, useful for one-off shutdown coordination
func ShutdownWithFunc(fn func(context.Context) error, timeout time.Duration) error {
	m := NewManager()
	m.Register(fn)
	return m.Shutdown(timeout)
}
