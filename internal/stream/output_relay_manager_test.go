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
