package stream

import (
	"sync"
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
		FFmpegArgs:     []string{"-f", "null", "-"},
	}
	// Inject a process that always fails
	_ = orm.StartOutputRelay(config)
	// Wait for the process to fail and callback to be called
	time.Sleep(300 * time.Millisecond)
	if atomic.LoadInt32(&called) == 0 {
		t.Errorf("expected failure callback to be called deterministically")
	}
}

func TestOutputRelayManager_ConcurrentAPI(t *testing.T) {
	t.Parallel()
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)

	num := 10
	var wg sync.WaitGroup
	configs := make([]OutputRelayConfig, num)
	for i := 0; i < num; i++ {
		configs[i] = OutputRelayConfig{
			OutputURL:      "rtmp://example.com/live/" + string(rune('A'+i)),
			OutputName:     "out" + string(rune('A'+i)),
			InputURL:       "rtsp://localhost/relay/" + string(rune('A'+i)),
			LocalURL:       "rtsp://localhost/relay/" + string(rune('A'+i)),
			Timeout:        500 * time.Millisecond,
			PlatformPreset: "",
			FFmpegOptions:  map[string]string{},
			FFmpegArgs:     []string{"-f", "null", "-"},
		}
	}

	// Start output relays concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(cfg OutputRelayConfig) {
			defer wg.Done()
			_ = orm.StartOutputRelay(cfg)
		}(configs[i])
	}

	// Stop output relays concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(cfg OutputRelayConfig) {
			defer wg.Done()
			orm.StopOutputRelay(cfg.OutputURL)
		}(configs[i])
	}

	// Delete output relays concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(cfg OutputRelayConfig) {
			defer wg.Done()
			_ = orm.DeleteOutput(cfg.OutputURL)
		}(configs[i])
	}

	wg.Wait()
}

// --- Additional coverage tests ---
func TestOutputRelayManager_StartOutputRelay_Duplicate(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	config := OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live/dup",
		OutputName:     "dupout",
		InputURL:       "rtsp://localhost/relay/dup",
		LocalURL:       "rtsp://localhost/relay/dup",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-f", "null", "-"},
	}
	_ = orm.StartOutputRelay(config)
	err := orm.StartOutputRelay(config)
	if err == nil {
		t.Errorf("expected error on duplicate StartOutputRelay")
	}
}

func TestOutputRelayManager_StopOutputRelay_NonExistent(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	// Should not panic or error
	orm.StopOutputRelay("nonexistent")
}

func TestOutputRelayManager_DeleteOutput_NonExistent(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	err := orm.DeleteOutput("nonexistent")
	if err == nil {
		t.Errorf("expected error deleting non-existent output relay")
	}
}

func TestOutputRelayManager_RunOutputRelay_ErrorBranches(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	outputURL := "rtmp://example.com/live/error"
	relay := &OutputRelay{
		OutputURL: outputURL,
		InputURL:  "rtsp://localhost/relay/error",
		LocalURL:  "rtsp://localhost/relay/error",
		Status:    OutputRunning,
		// Proc is nil
	}
	orm.mu.Lock()
	orm.Relays[outputURL] = relay
	orm.mu.Unlock()
	// Should handle nil Proc gracefully
	go orm.RunOutputRelay(relay)
	time.Sleep(50 * time.Millisecond)
}

func TestOutputRelayManager_StopOutputRelay_AlreadyStopped(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	config := OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live/stopped",
		OutputName:     "stoppedout",
		InputURL:       "rtsp://localhost/relay/stopped",
		LocalURL:       "rtsp://localhost/relay/stopped",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-f", "null", "-"},
	}
	_ = orm.StartOutputRelay(config)
	orm.StopOutputRelay(config.OutputURL)
	// Stop again (should be already stopped)
	orm.StopOutputRelay(config.OutputURL)
}

func TestOutputRelayManager_StartOutputRelay_InvalidConfig(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	// Empty OutputURL
	config := OutputRelayConfig{
		OutputURL:      "",
		OutputName:     "emptyout",
		InputURL:       "rtsp://localhost/relay/empty",
		LocalURL:       "rtsp://localhost/relay/empty",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-f", "null", "-"},
	}
	err := orm.StartOutputRelay(config)
	if err == nil {
		t.Errorf("expected error for empty OutputURL")
	}
	// Empty InputURL
	config = OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live/emptyinput",
		OutputName:     "emptyinputout",
		InputURL:       "",
		LocalURL:       "rtsp://localhost/relay/emptyinput",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-f", "null", "-"},
	}
	err = orm.StartOutputRelay(config)
	if err == nil {
		t.Errorf("expected error for empty InputURL")
	}
}

func TestOutputRelayManager_StartOutputRelay_FFmpegFail(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	// Use invalid FFmpeg args to force process creation failure
	config := OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live/ffmpegfail",
		OutputName:     "ffmpegfailout",
		InputURL:       "rtsp://localhost/relay/ffmpegfail",
		LocalURL:       "rtsp://localhost/relay/ffmpegfail",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-invalidflag"},
	}
	err := orm.StartOutputRelay(config)
	if err == nil {
		t.Errorf("expected error for FFmpeg process creation failure")
	}
}

func TestOutputRelayManager_StartOutputRelay_RestartStoppedOrError(t *testing.T) {
	log := logger.NewLogger()
	orm := NewOutputRelayManager(log)
	config := OutputRelayConfig{
		OutputURL:      "rtmp://example.com/live/restart",
		OutputName:     "restartout",
		InputURL:       "rtsp://localhost/relay/restart",
		LocalURL:       "rtsp://localhost/relay/restart",
		Timeout:        1 * time.Second,
		PlatformPreset: "",
		FFmpegOptions:  map[string]string{},
		FFmpegArgs:     []string{"-f", "null", "-"},
	}
	_ = orm.StartOutputRelay(config)
	// Simulate stopped relay
	orm.mu.Lock()
	relay := orm.Relays[config.OutputURL]
	relay.Status = OutputStopped
	orm.mu.Unlock()
	err := orm.StartOutputRelay(config)
	if err != nil {
		t.Errorf("expected no error restarting stopped relay, got %v", err)
	}
	// Simulate error relay
	orm.mu.Lock()
	relay.Status = OutputError
	orm.mu.Unlock()
	err = orm.StartOutputRelay(config)
	if err != nil {
		t.Errorf("expected no error restarting error relay, got %v", err)
	}
}
