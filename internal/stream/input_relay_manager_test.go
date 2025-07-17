package stream

import (
	"io"
	"os"
	"path/filepath"
	"sync"
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

	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	inputName := "test"
	inputURL := "file://" + relative
	localURL := rtspServer.GetRTSPURL("relay/test")
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
	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	inputName := "test"
	localURL := rtspServer.GetRTSPURL("relay/test")
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

func TestInputRelayManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	log := logger.NewLogger()
	dir := t.TempDir()
	irm := NewInputRelayManager(log, dir)

	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	num := 10
	var wg sync.WaitGroup
	inputNames := make([]string, num)
	inputURLs := make([]string, num)
	localURLs := make([]string, num)
	for i := 0; i < num; i++ {
		inputNames[i] = "input" + string(rune('A'+i))
		inputURLs[i] = "rtmp://example.com/live/" + string(rune('A'+i))
		localURLs[i] = rtspServer.GetRTSPURL("relay/" + string(rune('A'+i)))
	}
	timeout := 500 * time.Millisecond

	// Start input relays concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(name, inputURL, localURL string) {
			defer wg.Done()
			_, _ = irm.StartInputRelay(name, inputURL, localURL, timeout)
		}(inputNames[i], inputURLs[i], localURLs[i])
	}

	// Stop input relays concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(inputURL string) {
			defer wg.Done()
			irm.StopInputRelay(inputURL)
		}(inputURLs[i])
	}

	// Delete input relays concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(inputURL string) {
			defer wg.Done()
			_ = irm.DeleteInput(inputURL)
		}(inputURLs[i])
	}

	wg.Wait()
}

// --- Additional coverage tests ---
func TestInputRelayManager_ForceStopInputRelay(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)

	// Should not panic or error on non-existent relay
	irm.ForceStopInputRelay("nonexistent")

	// Create a relay and force stop it
	inputName := "test"
	inputURL := "rtmp://example.com/live/test"

	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	localURL := rtspServer.GetRTSPURL("relay/test")
	timeout := 1 * time.Second
	_, _ = irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	irm.ForceStopInputRelay(inputURL)
}

func TestInputRelayManager_GetInputNameForURL(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)
	inputName := "test"
	inputURL := "rtmp://example.com/live/test"

	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	localURL := rtspServer.GetRTSPURL("relay/test")
	timeout := 1 * time.Second
	_, _ = irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	name := irm.GetInputNameForURL(inputURL)
	if name != inputName {
		t.Errorf("expected %s, got %s", inputName, name)
	}
	// Non-existent URL
	if irm.GetInputNameForURL("nonexistent") != "" {
		t.Errorf("expected empty string for non-existent URL")
	}
}

func TestInputRelayManager_RunInputRelay_ErrorBranches(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)
	// Create a relay struct manually with nil process to force error
	inputURL := "rtmp://example.com/live/test"
	relay := &InputRelay{
		InputName: inputURL,
		InputURL:  inputURL,
		LocalURL:  "rtsp://localhost:8554/relay/test",
		Status:    InputRunning,
		RefCount:  1,
		// Proc is nil
	}
	irm.mu.Lock()
	irm.Relays[inputURL] = relay
	irm.mu.Unlock()
	// Should handle nil Proc gracefully
	go irm.RunInputRelay(relay)
	time.Sleep(50 * time.Millisecond)
}

func TestInputRelayManager_StopInputRelay_AlreadyStopped(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)
	inputName := "test"
	inputURL := "rtmp://example.com/live/test"

	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	localURL := rtspServer.GetRTSPURL("relay/test")
	timeout := 1 * time.Second
	_, _ = irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	// Stop relay
	irm.StopInputRelay(inputURL)
	// Stop again (should be already stopped)
	irm.StopInputRelay(inputURL)
}

func TestInputRelayManager_StartInputRelay_InvalidURL(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)
	inputName := "test"
	inputURL := "file://doesnotexist.mp4"

	rtspServer := NewRTSPServerManagerWithConfig(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	irm.SetRTSPServer(rtspServer)

	localURL := rtspServer.GetRTSPURL("relay/test")
	timeout := 1 * time.Second
	_, err := irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	if err == nil {
		t.Errorf("expected error for missing file inputURL")
	}
}
