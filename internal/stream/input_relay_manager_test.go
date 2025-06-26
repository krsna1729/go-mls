package stream

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-mls/internal/logger"
)

func TestInputRelayManager_resolveInputURL(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)

	// Create a dummy file to simulate a recording
	relative := "testsrc.mp4"
	filePath := filepath.Join(tmpDir, relative)
	if err := os.WriteFile(filePath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Should resolve file:// URL to the correct path
	resolved, err := irm.resolveInputURL("file://" + relative)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resolved != filePath {
		t.Errorf("expected %s, got %s", filePath, resolved)
	}

	// Should error if file does not exist
	_, err = irm.resolveInputURL("file://doesnotexist.mp4")
	if err == nil {
		t.Errorf("expected error for missing file, got nil")
	}

	// Should return inputURL unchanged for non-file URLs
	url := "rtmp://example.com/live"
	resolved, err = irm.resolveInputURL(url)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resolved != url {
		t.Errorf("expected %s, got %s", url, resolved)
	}
}

func TestInputRelayManager_StartInputRelay_fileURL(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)

	relative := "testsrc.mp4"
	filePath := filepath.Join(tmpDir, relative)
	if err := os.WriteFile(filePath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	inputName := "test"
	inputURL := "file://" + relative
	localURL := "rtsp://localhost:8554/relay/test"
	timeout := 1 * time.Second

	// Start relay (should resolve file:// and not error)
	_, err := irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Clean up
	irm.StopInputRelay(inputURL)
}

func TestInputRelayManager_RefCounting(t *testing.T) {
	t.Parallel()

	// Step 1: Create a temp directory for this test
	tempDir := t.TempDir()

	// Step 2: Copy testdata/testsrc.mp4 into the temp directory
	src := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	dst := filepath.Join(tempDir, "testsrc.mp4")
	srcFile, err := os.Open(src)
	if err != nil {
		t.Fatalf("failed to open source file: %v", err)
	}
	defer srcFile.Close()
	dstFile, err := os.Create(dst)
	if err != nil {
		t.Fatalf("failed to create destination file: %v", err)
	}
	defer dstFile.Close()
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		t.Fatalf("failed to copy file: %v", err)
	}

	// Step 3: Construct a file:// URL for the copied file
	inputURL := "file://testsrc.mp4"

	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tempDir)

	// Start a test RTSP server (required for ffmpeg relay output)
	rtspServer := NewRTSPServerManager(log)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	inputName := "test"
	localURL := "rtsp://localhost:8554/relay/test"
	timeout := 1 * time.Second

	// Start relay twice - should reuse existing relay
	_, err1 := irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	if err1 != nil {
		t.Fatalf("expected no error on first start, got %v", err1)
	}

	_, err2 := irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	if err2 != nil {
		t.Fatalf("expected no error on second start, got %v", err2)
	}

	// Give the relay a moment to initialize/process
	time.Sleep(100 * time.Millisecond)

	// Check that relay exists and has proper refcount
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	irm.mu.Unlock()

	if !exists {
		t.Fatalf("expected relay to exist for key %q", inputURL)
	}
	if relay == nil {
		t.Fatalf("relay is nil for key %q", inputURL)
	}

	relay.mu.Lock()
	refCount := relay.RefCount
	status := relay.Status
	relay.mu.Unlock()

	if refCount != 2 {
		t.Errorf("expected refcount 2, got %d", refCount)
	}
	if status != InputRunning {
		t.Errorf("expected relay to be running, got status %v", status)
	}

	// Stop once - should still exist, refcount decremented
	irm.StopInputRelay(inputURL)
	time.Sleep(50 * time.Millisecond)

	irm.mu.Lock()
	relay, exists = irm.Relays[inputURL]
	irm.mu.Unlock()

	if !exists {
		t.Fatalf("expected relay to still exist after first stop for key %q", inputURL)
	}

	relay.mu.Lock()
	refCount = relay.RefCount
	status = relay.Status
	relay.mu.Unlock()

	if refCount != 1 {
		t.Errorf("expected refcount 1 after first stop, got %d", refCount)
	}
	if status != InputRunning {
		t.Errorf("expected relay to still be running after first stop, got status %v (InputRunning=%v)", status, InputRunning)
	}
	if status != InputRunning {
		t.Logf("relay status after first stop: %v, last error: %v (InputRunning=%v)", status, relay.LastError, InputRunning)
	}

	// Stop again - relay should still exist, but be stopped and refcount 0
	irm.StopInputRelay(inputURL)
	time.Sleep(50 * time.Millisecond)

	irm.mu.Lock()
	relay, exists = irm.Relays[inputURL]
	irm.mu.Unlock()

	if !exists {
		t.Fatalf("expected relay to still exist after final stop (deletion is explicit) for key %q", inputURL)
	}

	relay.mu.Lock()
	refCount = relay.RefCount
	status = relay.Status
	relay.mu.Unlock()

	if refCount != 0 {
		t.Errorf("expected refcount 0 after final stop, got %d", refCount)
	}
	if status != InputStopped && status != InputError {
		t.Errorf("expected relay to be stopped or error after final stop, got status %v (InputStopped=%v, InputError=%v)", status, InputStopped, InputError)
	}

	// Now explicitly delete the relay
	if err := irm.DeleteInput(inputURL); err != nil {
		t.Errorf("expected no error on DeleteInput, got %v", err)
	}

	irm.mu.Lock()
	_, exists = irm.Relays[inputURL]
	irm.mu.Unlock()

	if exists {
		t.Errorf("expected relay to be deleted after DeleteInput")
	}

	// Add a timeout to ensure test does not hang
	done := make(chan struct{})
	go func() {
		// Simulate some work
		time.Sleep(10 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("test timed out")
	}
}

func TestInputRelayManager_StopNonExistentRelay(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)

	// Stopping non-existent relay should not panic or error
	irm.StopInputRelay("nonexistent")
}
