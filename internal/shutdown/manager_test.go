package shutdown

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("Expected non-nil manager")
	}
	if m.ctx == nil {
		t.Error("Expected non-nil context")
	}
	if m.cancel == nil {
		t.Error("Expected non-nil cancel function")
	}
}

func TestContext(t *testing.T) {
	m := NewManager()
	ctx := m.Context()
	if ctx == nil {
		t.Fatal("Expected non-nil context")
	}

	// Context should not be canceled initially
	select {
	case <-ctx.Done():
		t.Error("Context should not be canceled initially")
	default:
		// Expected
	}

	// After shutdown, context should be canceled
	_ = m.Shutdown(1 * time.Second)
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be canceled after shutdown")
	}
}

func TestRegisterAndShutdown(t *testing.T) {
	m := NewManager()
	var called int32

	m.Register(func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	m.Register(func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	err := m.Shutdown(5 * time.Second)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if atomic.LoadInt32(&called) != 2 {
		t.Errorf("Expected 2 functions called, got %d", called)
	}
}

func TestShutdownWithError(t *testing.T) {
	m := NewManager()

	m.Register(func(ctx context.Context) error {
		return nil
	})

	m.Register(func(ctx context.Context) error {
		return fmt.Errorf("test error")
	})

	err := m.Shutdown(5 * time.Second)
	if err == nil {
		t.Error("Expected error from shutdown")
	}
}

func TestShutdownTimeout(t *testing.T) {
	m := NewManager()

	m.Register(func(ctx context.Context) error {
		// Simulate a long-running shutdown that ignores context
		time.Sleep(2 * time.Second)
		return nil
	})

	start := time.Now()
	err := m.Shutdown(100 * time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Expected timeout error")
	}

	// Should timeout around 100ms, not wait for full 2 seconds
	if elapsed > 500*time.Millisecond {
		t.Errorf("Shutdown took too long: %v", elapsed)
	}
}

func TestShutdownRespectsContext(t *testing.T) {
	m := NewManager()
	var completed int32

	m.Register(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&completed, 1)
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("function did not respect context")
		}
	})

	err := m.Shutdown(100 * time.Millisecond)
	if err == nil {
		t.Error("Expected timeout error")
	}

	// Give a moment for the goroutine to complete
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&completed) != 1 {
		t.Error("Function did not complete via context cancellation")
	}
}

func TestShutdownWithFunc(t *testing.T) {
	var called int32

	err := ShutdownWithFunc(func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	}, 1*time.Second)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("Expected function to be called once, got %d", called)
	}
}

func TestMultipleErrors(t *testing.T) {
	m := NewManager()

	m.Register(func(ctx context.Context) error {
		return fmt.Errorf("error 1")
	})

	m.Register(func(ctx context.Context) error {
		return fmt.Errorf("error 2")
	})

	m.Register(func(ctx context.Context) error {
		return nil
	})

	err := m.Shutdown(5 * time.Second)
	if err == nil {
		t.Error("Expected error from shutdown")
	}

	// Error should mention multiple errors
	errStr := err.Error()
	if errStr == "" {
		t.Error("Expected non-empty error string")
	}
}

func TestShutdownOrder(t *testing.T) {
	m := NewManager()
	var order []int
	var mu sync.Mutex

	// Register functions that record their execution order
	for i := 0; i < 5; i++ {
		idx := i
		m.Register(func(ctx context.Context) error {
			mu.Lock()
			order = append(order, idx)
			mu.Unlock()
			// Small delay to ensure concurrent execution
			time.Sleep(10 * time.Millisecond)
			return nil
		})
	}

	err := m.Shutdown(5 * time.Second)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(order) != 5 {
		t.Errorf("Expected 5 functions to execute, got %d", len(order))
	}

	// Note: Since functions run concurrently, we just verify they all ran
	// We don't enforce ordering since they execute in parallel
}
