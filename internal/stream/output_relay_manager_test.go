package stream

import (
	"go-mls/internal/logger"
	"testing"
	"time"
)

func TestOutputRelayManager_StartAndStopOutputRelay(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)

	inputURL := "rtsp://localhost/relay/test"
	localURL := inputURL
	outputURL := "rtmp://example.com/live"
	outputName := "test_output"
	ffmpegArgs := []string{"-re", "-i", localURL, "-c", "copy", "-f", "flv", outputURL}
	timeout := 1 * time.Second

	config := OutputRelayConfig{
		OutputURL:      outputURL,
		OutputName:     outputName,
		InputURL:       inputURL,
		LocalURL:       localURL,
		Timeout:        timeout,
		PlatformPreset: "",
		FFmpegOptions:  make(map[string]string),
		FFmpegArgs:     ffmpegArgs,
	}

	err := orm.StartOutputRelay(config)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Should not start a second relay for the same outputURL
	err = orm.StartOutputRelay(config)
	if err != nil {
		t.Errorf("expected no error for duplicate start, got %v", err)
	}

	// Stop relay
	orm.StopOutputRelay(outputURL)
	orm.mu.Lock()
	relay, exists := orm.Relays[outputURL]
	orm.mu.Unlock()
	if !exists {
		t.Errorf("expected relay to exist after stop")
	}
	if relay.Status != OutputStopped {
		t.Errorf("expected status OutputStopped, got %v", relay.Status)
	}
}

func TestOutputRelayManager_StopNonExistentRelay(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)

	// Stopping non-existent relay should not panic
	orm.StopOutputRelay("nonexistent")

	// Should have no relays
	orm.mu.Lock()
	count := len(orm.Relays)
	orm.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 relays, got %d", count)
	}
}

func TestOutputRelayManager_GetRelayStatus(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)

	// Start a relay and test status
	config := OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live",
		OutputName:     "test",
		InputURL:       "rtsp://localhost/relay/test",
		LocalURL:       "rtsp://localhost/relay/test",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  make(map[string]string),
		FFmpegArgs:     []string{"-re", "-i", "rtsp://localhost/relay/test", "-c", "copy", "-f", "flv", "rtmp://example.com/live"},
	}

	err := orm.StartOutputRelay(config)
	if err != nil {
		t.Errorf("expected no error starting relay, got %v", err)
	}

	// Check relay exists in manager
	orm.mu.Lock()
	relay, exists := orm.Relays["rtmp://example.com/live"]
	orm.mu.Unlock()

	if !exists {
		t.Errorf("expected relay to exist after start")
	}

	if relay.Status == OutputStopped {
		t.Errorf("expected relay status to not be OutputStopped")
	}

	// Clean up
	orm.StopOutputRelay("rtmp://example.com/live")
}
