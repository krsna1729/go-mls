package stream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-mls/internal/logger"
)

func TestInputRelayManager_resolveInputURL(t *testing.T) {
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
	localURL := "rtsp://localhost/relay/test"
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
	localURL := "rtsp://localhost/relay/test"
	timeout := 1 * time.Second

	// Start relay twice - should reuse existing relay
	_, err1 := irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	if err1 != nil {
		t.Errorf("expected no error on first start, got %v", err1)
	}

	_, err2 := irm.StartInputRelay(inputName, inputURL, localURL, timeout)
	if err2 != nil {
		t.Errorf("expected no error on second start, got %v", err2)
	}

	// Give the relay a moment to initialize
	time.Sleep(100 * time.Millisecond)

	// Check that relay exists and has proper refcount
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL] // Use inputURL as key, not inputName
	irm.mu.Unlock()

	if !exists {
		t.Errorf("expected relay to exist")
	}

	if relay == nil {
		t.Errorf("relay is nil")
		return
	}

	relay.mu.Lock()
	refCount := relay.RefCount
	relay.mu.Unlock()

	if refCount != 2 {
		t.Errorf("expected refcount 2, got %d", refCount)
	}

	// Stop once - should still be running
	irm.StopInputRelay(inputURL)

	time.Sleep(50 * time.Millisecond)

	irm.mu.Lock()
	relay, exists = irm.Relays[inputURL]
	irm.mu.Unlock()

	if !exists {
		t.Errorf("expected relay to still exist after first stop")
	}

	relay.mu.Lock()
	refCount = relay.RefCount
	relay.mu.Unlock()

	if refCount != 1 {
		t.Errorf("expected refcount 1 after first stop, got %d", refCount)
	}

	// Stop again - should clean up
	irm.StopInputRelay(inputURL)

	time.Sleep(50 * time.Millisecond)

	irm.mu.Lock()
	_, exists = irm.Relays[inputURL]
	irm.mu.Unlock()

	if exists {
		t.Errorf("expected relay to be cleaned up after final stop")
	}
}

func TestInputRelayManager_StopNonExistentRelay(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.NewLogger()
	irm := NewInputRelayManager(log, tmpDir)

	// Stopping non-existent relay should not panic or error
	irm.StopInputRelay("nonexistent")
}
