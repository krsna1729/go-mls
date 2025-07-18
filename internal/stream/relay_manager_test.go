package stream

import (
	"context"
	"go-mls/internal/logger"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestRelayManager() *RelayManager {
	// Use correct timeouts: 5 seconds as time.Duration
	rl := NewRelayManager(logger.NewLoggerWithConfig("debug", ""), os.TempDir(), "error")
	rl.SetRTSPServer(NewRTSPServerManager(logger.NewLoggerWithConfig("debug", ""), DefaultRTSPInterface, DefaultRTSPPort))
	// Set input/output timeouts to 5 seconds (time.Duration)
	rl.SetTimeouts(5*time.Second, 5*time.Second)
	return rl
}

func newTestRelayManagerWithRTSP(t *testing.T) (*RelayManager, *RTSPServerManager) {
	t.Helper()
	log := logger.NewLoggerWithConfig("debug", "")
	dir := t.TempDir()
	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	rl := NewRelayManager(log, dir, "error")
	rl.SetRTSPServer(rtspServer)
	// Set input/output timeouts to 5 seconds
	rl.SetTimeouts(5*time.Second, 5*time.Second)
	return rl, rtspServer
}

func newTestRelayManagerWithTimeout(timeout time.Duration) *RelayManager {
	// Use correct timeouts: timeout as time.Duration
	rl := NewRelayManager(logger.NewLoggerWithConfig("debug", ""), os.TempDir(), "error")
	rl.SetRTSPServer(NewRTSPServerManager(logger.NewLoggerWithConfig("debug", ""), DefaultRTSPInterface, DefaultRTSPPort))
	rl.SetTimeouts(timeout, timeout)
	return rl
}

func TestStartRelayWithOptions_Basic(t *testing.T) {
	t.Parallel()
	log := logger.NewLoggerWithConfig("debug", "")
	dir := t.TempDir()

	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	rl := NewRelayManager(log, dir, "error")
	rl.SetRTSPServer(rtspServer)

	// Copy testsrc.mp4 to temp dir and chdir
	testSrcPath := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	testDestPath := filepath.Join(dir, "testsrc.mp4")
	srcFile, err := os.Open(testSrcPath)
	if err != nil {
		t.Fatalf("failed to open testsrc.mp4: %v", err)
	}
	defer srcFile.Close()
	destFile, err := os.Create(testDestPath)
	if err != nil {
		t.Fatalf("failed to create dest testsrc.mp4: %v", err)
	}
	defer destFile.Close()
	_, _ = io.Copy(destFile, srcFile)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	inputName := "testInput"
	outputName := "testOutput"
	inputURL := "file://testsrc.mp4"
	rl.RegisterInputConfig(inputName, inputURL)
	outputURL := "rtmp://localhost/live/test"
	opts := &FFmpegOptions{VideoCodec: "libx264"}
	// Start relay
	err = rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, opts, "")
	if err == nil {
		rl.StopRelay(inputURL, outputURL, inputName, outputName)
	} else {
		t.Fatalf("StartRelayWithOptions failed: %v", err)
	}
}

func TestStopRelay_NoPanic(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	// Should not panic or error even if relay does not exist
	err := rl.StopRelay("input", "output", "in", "out")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDeleteInput_NoPanic(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	err := rl.DeleteInput("input", "in")
	if err == nil {
		return // success, input not present is fine
	}
	if err.Error() != "input relay not found: input" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeleteOutput_NoPanic(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	err := rl.DeleteOutput("input", "output", "in", "out")
	if err == nil {
		return // success, output not present is fine
	}
	if err.Error() != "output relay not found: output" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExportImportConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	file := "test_relay_export.json"
	defer os.Remove(file)
	if err := rl.ExportConfig(file); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if err := rl.ImportConfig(file); err != nil {
		t.Fatalf("import failed: %v", err)
	}
}

func TestGetEndpointConfig_NotFound(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	_, _, err := rl.GetEndpointConfig("input", "output")
	if err == nil {
		t.Errorf("expected error for missing endpoint config")
	}
}

func TestInputRelayStatusString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in  InputRelayStatus
		exp string
	}{
		{InputStarting, "Starting"},
		{InputRunning, "Running"},
		{InputError, "Error"},
		{InputStopped, "Stopped"},
	}
	for _, c := range cases {
		if got := inputRelayStatusString(c.in); got != c.exp {
			t.Errorf("got %q, want %q", got, c.exp)
		}
	}
}

func TestOutputRelayStatusString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in  OutputRelayStatus
		exp string
	}{
		{OutputStarting, "Starting"},
		{OutputRunning, "Running"},
		{OutputError, "Error"},
		{OutputStopped, "Stopped"},
	}
	for _, c := range cases {
		if got := outputRelayStatusString(c.in); got != c.exp {
			t.Errorf("got %q, want %q", got, c.exp)
		}
	}
}

func TestStopAllRelays_NoPanic(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	// Should not panic or deadlock
	rl.StopAllRelays()
}

func TestSetAndGetTimeouts(t *testing.T) {
	rl := newTestRelayManager()
	rl.SetTimeouts(42, 99)
	if rl.GetInputTimeout() != 42 {
		t.Errorf("expected input timeout 42, got %v", rl.GetInputTimeout())
	}
}

func TestExportConfig_Error(t *testing.T) {
	rl := newTestRelayManager()
	err := rl.ExportConfig("/not/a/real/dir/shouldfail.json")
	if err == nil {
		t.Errorf("expected error for bad export path")
	}
}

func TestImportConfig_Error(t *testing.T) {
	rl := newTestRelayManager()
	f, err := os.CreateTemp("", "bad.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("not json")
	f.Close()
	err = rl.ImportConfig(f.Name())
	if err == nil {
		t.Errorf("expected error for bad import json")
	}
}

func TestGetEndpointConfig_OutputExists(t *testing.T) {
	t.Parallel()
	log := logger.NewLoggerWithConfig("debug", "")
	dir := t.TempDir()

	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	rl := NewRelayManager(log, dir, "error")
	rl.SetRTSPServer(rtspServer)

	// Copy testsrc.mp4 to temp dir and chdir
	testSrcPath := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	testDestPath := filepath.Join(dir, "testsrc.mp4")
	srcFile, err := os.Open(testSrcPath)
	if err != nil {
		t.Fatalf("failed to open testsrc.mp4: %v", err)
	}
	defer srcFile.Close()
	destFile, err := os.Create(testDestPath)
	if err != nil {
		t.Fatalf("failed to create dest testsrc.mp4: %v", err)
	}
	defer destFile.Close()
	_, _ = io.Copy(destFile, srcFile)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	inputName := "testInput"
	inputURL := "file://testsrc.mp4"
	rl.RegisterInputConfig(inputName, inputURL)
	// Start input relay to create RTSP stream
	if _, err := rl.StartInputRelayForConsumer(inputName); err != nil {
		t.Fatalf("failed to start input relay: %v", err)
	}

	// Start output relay using the file input URL and non-nil FFmpegOptions
	outURL := "rtmp://example.com/live/test"
	outName := "out"
	opts := &FFmpegOptions{VideoCodec: "libx264"}
	_ = rl.OutputRelays.StartOutputRelay(OutputRelayConfig{
		OutputURL:     outURL,
		OutputName:    outName,
		InputURL:      inputURL,
		LocalURL:      "local",
		Timeout:       1,
		FFmpegOptions: map[string]string{"video_codec": opts.VideoCodec},
	})
	preset, gotOpts, err := rl.GetEndpointConfig(inputURL, outURL)
	if err != nil {
		t.Errorf("expected found, got err: %v", err)
	}
	if preset != "" {
		t.Errorf("expected default preset, got %v", preset)
	}
	if gotOpts == nil {
		t.Errorf("expected non-nil opts, got nil")
	}
}

func TestStatusV2_WithRelays(t *testing.T) {
	rl := newTestRelayManager()
	// Simulate input relay
	inURL := "rtsp://localhost:8554/test"
	rl.InputRelays.Relays[inURL] = &InputRelay{
		InputURL:  inURL,
		InputName: "in",
		LocalURL:  "local",
		Status:    InputRunning,
	}
	status := rl.StatusV2()
	if len(status.Relays) != 1 {
		t.Errorf("expected 1 relay, got %d", len(status.Relays))
	}
}

func TestStopAllRelays_ForceStop(t *testing.T) {
	rl := newTestRelayManager()
	inURL := "rtsp://localhost:8554/test"
	inName := "in"
	rl.InputRelays.Relays[inURL] = &InputRelay{
		InputURL:  inURL,
		InputName: inName,
		LocalURL:  "local",
		Status:    InputRunning,
		RefCount:  1,
	}
	rl.StopAllRelays()
}

func TestStartRelayWithOptions_InputRelayError(t *testing.T) {
	t.Parallel()
	// Use a relay manager with a very short timeout for fast failure
	rl := newTestRelayManagerWithTimeout(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- rl.StartRelayWithOptions("badinput", "badoutput", "badname", "badout", nil, "")
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Errorf("expected error for bad input relay")
		}
	case <-ctx.Done():
		t.Fatalf("test timed out waiting for error from StartRelayWithOptions")
	}
}
