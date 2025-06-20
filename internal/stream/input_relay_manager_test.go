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
}
