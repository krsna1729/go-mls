package stream

import (
	"sync/atomic"
	"testing"
	"time"

	"go-mls/internal/logger"
)

func TestOutputRelayManager_StartStopDelete(t *testing.T) {
	t.Parallel()
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	config := OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live",
		OutputName:     "testout",
		InputURL:       "rtsp://localhost/relay/test",
		LocalURL:       "rtsp://localhost/relay/test",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-f", "null", "-"}, // Use dummy args for test
	}

	err := orm.StartOutputRelay(config)
	if err != nil {
		t.Fatalf("expected no error starting output relay, got %v", err)
	}

	// Should exist in map
	orm.mu.Lock()
	relay, exists := orm.Relays[config.OutputURL]
	orm.mu.Unlock()
	if !exists || relay == nil {
		t.Fatalf("expected relay to exist after start")
	}

	// Stop relay
	orm.StopOutputRelay(config.OutputURL)
	orm.mu.Lock()
	relay, exists = orm.Relays[config.OutputURL]
	orm.mu.Unlock()
	if !exists || relay == nil {
		t.Fatalf("expected relay to exist after stop (not deleted)")
	}

	// Delete relay
	err = orm.DeleteOutput(config.OutputURL)
	if err != nil {
		t.Fatalf("expected no error deleting output relay, got %v", err)
	}
	orm.mu.Lock()
	_, exists = orm.Relays[config.OutputURL]
	orm.mu.Unlock()
	if exists {
		t.Fatalf("expected relay to be deleted")
	}
}

func TestOutputRelayManager_FailureCallback(t *testing.T) {
	t.Parallel()
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	var called int32
	orm.SetFailureCallback(func(inputURL, outputURL string) {
		atomic.AddInt32(&called, 1)
	})
	config := OutputRelayConfig{
		OutputURL:      "rtmp://fail.example.com/live",
		OutputName:     "failout",
		InputURL:       "rtsp://localhost/relay/fail",
		LocalURL:       "rtsp://localhost/relay/fail",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-invalidflag"}, // Invalid arg to force ffmpeg failure
	}
	_ = orm.StartOutputRelay(config)
	// Wait for the process to fail and callback to be called
	time.Sleep(300 * time.Millisecond)
	if atomic.LoadInt32(&called) == 0 {
		t.Errorf("expected failure callback to be called")
	}
}
