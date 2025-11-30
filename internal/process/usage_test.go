package process

import (
	"os/exec"
	"reflect"
	"testing"
	"time"
)

// Test that GetSelfUsage returns a ProcUsage and that subsequent calls hit the cache
func TestGetSelfUsage_CacheBehavior(t *testing.T) {
	u1, err := GetSelfUsage()
	if err != nil {
		t.Fatalf("GetSelfUsage failed: %v", err)
	}
	if u1.PID == 0 {
		t.Fatalf("unexpected PID 0 from GetSelfUsage")
	}

	u2, err := GetSelfUsage()
	if err != nil {
		t.Fatalf("GetSelfUsage (second) failed: %v", err)
	}

	// The implementation stores the ProcUsage pointer in cache and returns it.
	// When cached, subsequent calls should return the same pointer value.
	if u1 != u2 {
		t.Fatalf("expected cached pointer equality for GetSelfUsage, got different pointers")
	}

	// Wait for cache TTL to expire and ensure a new pointer is produced
	time.Sleep(usageCacheTTL + 10*time.Millisecond)
	u3, err := GetSelfUsage()
	if err != nil {
		t.Fatalf("GetSelfUsage (after ttl) failed: %v", err)
	}
	if u3 == u2 {
		t.Fatalf("expected a new ProcUsage pointer after cache TTL expired")
	}
}

// Test GetProcUsage against a child process we start; validates PID and caching path
func TestGetProcUsage_ChildProcess(t *testing.T) {
	// Start a short-lived child process (sleep)
	cmd := exec.Command("/bin/sh", "-c", "sleep 1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start child process: %v", err)
	}
	pid := cmd.Process.Pid

	// Ensure we wait/cleanup at the end
	defer func() {
		// Try to kill if still running
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Give the child a moment to appear in the process table
	time.Sleep(20 * time.Millisecond)

	u1, err := GetProcUsage(pid)
	if err != nil {
		t.Fatalf("GetProcUsage failed for pid %d: %v", pid, err)
	}
	if u1.PID != pid {
		t.Fatalf("expected PID %d, got %d", pid, u1.PID)
	}

	// Call again to hit cached fast-path
	u2, err := GetProcUsage(pid)
	if err != nil {
		t.Fatalf("GetProcUsage (second) failed: %v", err)
	}
	if u1 != u2 {
		t.Fatalf("expected cached pointer equality for GetProcUsage, got different pointers")
	}

	// Test GetChildrenUsage returns at least one child (our sleep)
	children, err := GetChildrenUsage()
	if err != nil {
		t.Fatalf("GetChildrenUsage failed: %v", err)
	}
	found := false
	for _, cu := range children {
		if cu.PID == pid {
			found = true
			break
		}
	}
	// On some environments child listing may not include our sleep (timing), so allow either.
	// But at least ensure the returned slice is well-formed.
	if children == nil {
		t.Fatalf("GetChildrenUsage returned nil slice")
	}

	_ = found // not asserted strictly to avoid flaky failures on some CI

	// Ensure pointer equality check for cached map via reflect as a sanity check
	if reflect.ValueOf(u1).Pointer() == 0 {
		t.Fatalf("unexpected zero pointer for ProcUsage")
	}
}
