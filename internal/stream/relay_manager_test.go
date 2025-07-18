package stream

import (
	"context"
	"go-mls/internal/logger"
	"os"
	"path/filepath"
	"strings"
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
	dir, _ := copyTestSrcToTempDir(t)
	chdirTo(t, dir)

	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	rl := NewRelayManager(log, dir, "error")
	rl.SetRTSPServer(rtspServer)

	inputName := "testInput"
	outputName := "testOutput"
	inputURL := "file://testsrc.mp4"
	outputURL := "rtmp://localhost/live/test"
	opts := &FFmpegOptions{VideoCodec: "libx264"}
	// Start relay
	err := rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, opts, "")
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

func TestExportConfig_AndImportConfig_RoundTripWithRelays(t *testing.T) {
	t.Parallel()
	// Setup file-based input using helpers
	rl := newTestRelayManager()
	inputName := "testInput"
	outputName := "testOutput"
	dir, _ := copyTestSrcToTempDir(t)
	chdirTo(t, dir)
	inputURL := "file://testsrc.mp4"
	outputURL := "rtmp://localhost/live/test"
	// Register input config and relays
	rl.RegisterInputConfig(inputName, inputURL)
	rl.InputRelays.Relays[inputURL] = &InputRelay{
		InputURL:  inputURL,
		InputName: inputName,
		LocalURL:  "local",
		Status:    InputRunning,
	}
	rl.OutputRelays.Relays[outputURL] = &OutputRelay{
		OutputURL:  outputURL,
		OutputName: outputName,
		InputURL:   inputURL,
		LocalURL:   "local",
		Status:     OutputRunning,
	}
	file := filepath.Join(dir, "test_relay_export_roundtrip.json")
	defer os.Remove(file)
	if err := rl.ExportConfig(file); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	// Clear relays and configs
	rl.InputRelays.Relays = make(map[string]*InputRelay)
	rl.OutputRelays.Relays = make(map[string]*OutputRelay)
	rl.inputConfigs = make(map[string]*InputConfig)
	// Ensure testsrc.mp4 is present before import (already present in dir)
	// Start RTSP server before import
	log := logger.NewLoggerWithConfig("debug", "")
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()
	rl.SetRTSPServer(rtspServer)
	if err := rl.ImportConfig(file); err != nil {
		t.Fatalf("import failed: %v", err)
	}
	// Confirm relays and configs restored
	if _, ok := rl.inputConfigs[inputName]; !ok {
		t.Errorf("input config not restored after import")
	}
	if _, ok := rl.InputRelays.Relays[inputURL]; !ok {
		t.Errorf("input relay not restored after import")
	}
	if _, ok := rl.OutputRelays.Relays[outputURL]; !ok {
		t.Errorf("output relay not restored after import")
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

	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	// Setup file using helpers
	dir, _ := copyTestSrcToTempDir(t)
	chdirTo(t, dir)

	rl := NewRelayManager(log, dir, "error")
	rl.SetRTSPServer(rtspServer)

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

func TestGetRTSPServer_Getter(t *testing.T) {
	rl := newTestRelayManager()
	rs := rl.GetRTSPServer()
	if rs == nil {
		t.Errorf("expected non-nil RTSP server")
	}
	// Should match the server set by SetRTSPServer
	if rs != rl.rtspServer {
		t.Errorf("GetRTSPServer did not return the correct instance")
	}
}

func TestGetRTSPServerURL(t *testing.T) {
	url := GetRTSPServerURL()
	exp := "rtsp://127.0.0.1:8554"
	if url != exp {
		t.Errorf("expected %s, got %s", exp, url)
	}
}

func TestStartRelayWithOptions_ErrorBranches(t *testing.T) {
	t.Parallel()
	log := logger.NewLoggerWithConfig("debug", "")

	// Setup file using helpers for later use
	tempDir, _ := copyTestSrcToTempDir(t)

	rl := NewRelayManager(log, tempDir, "error")
	// Error branch: missing RTSP server
	rm := NewRelayManager(log, tempDir, "error")
	inputName := "testInput"
	outputName := "testOutput"
	inputURL := "file://nonexistent.mp4"
	outputURL := "rtmp://localhost/live/test"
	preset := ""
	options := &FFmpegOptions{}

	rm.inputConfigs[inputName] = &InputConfig{InputURL: inputURL, InputName: inputName}

	err := rm.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, options, preset)
	if err == nil || !strings.Contains(err.Error(), "RTSP server manager is not initialized") {
		t.Errorf("Expected error for missing RTSP server manager, got: %v", err)
	}

	// Set RTSP server, but input config not registered
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	_ = rtspServer.Start()
	defer rtspServer.Stop()
	rl.SetRTSPServer(rtspServer)
	err = rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, options, "")
	if err == nil {
		t.Errorf("expected error for missing input config")
	}

	// Register input config, but file does not exist
	rl.RegisterInputConfig(inputName, inputURL)
	err = rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, options, "")
	if err == nil {
		t.Errorf("expected error for missing input file")
	}

	// Register input config, valid file, but duplicate relay
	// Setup valid file
	chdirTo(t, tempDir)
	inputURL = "file://testsrc.mp4"
	rl.RegisterInputConfig(inputName, inputURL)
	err = rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, options, "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Try to start again (should error: relay already running)
	err = rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, options, "")
	if err == nil {
		t.Errorf("expected error for duplicate relay")
	}
}

func TestDeleteInput_RemovesRelay(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	inputName := "testInput"
	inputURL := "rtsp://localhost:8554/test"
	rl.RegisterInputConfig(inputName, inputURL)
	// Simulate input relay
	rl.InputRelays.Relays[inputURL] = &InputRelay{
		InputURL:  inputURL,
		InputName: inputName,
		LocalURL:  "local",
		Status:    InputRunning,
	}
	// Confirm relay exists
	if _, ok := rl.InputRelays.Relays[inputURL]; !ok {
		t.Fatalf("input relay not registered")
	}
	// Delete input
	err := rl.DeleteInput(inputURL, inputName)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// Confirm relay is removed
	if _, ok := rl.InputRelays.Relays[inputURL]; ok {
		t.Errorf("input relay not removed after DeleteInput")
	}
}

func TestDeleteOutput_RemovesRelay(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	inputName := "testInput"
	outputName := "testOutput"
	inputURL := "rtsp://localhost:8554/test"
	outputURL := "rtmp://localhost/live/test"
	// Simulate output relay
	rl.OutputRelays.Relays[outputURL] = &OutputRelay{
		OutputURL:  outputURL,
		OutputName: outputName,
		InputURL:   inputURL,
		LocalURL:   "local",
		Status:     OutputRunning,
	}
	// Confirm relay exists
	if _, ok := rl.OutputRelays.Relays[outputURL]; !ok {
		t.Fatalf("output relay not registered")
	}
	// Delete output
	err := rl.DeleteOutput(inputURL, outputURL, inputName, outputName)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// Confirm relay is removed
	if _, ok := rl.OutputRelays.Relays[outputURL]; ok {
		t.Errorf("output relay not removed after DeleteOutput")
	}
}

func TestStartRelayWithOptions_AllFFmpegOptions(t *testing.T) {
	t.Parallel()
	log := logger.NewLoggerWithConfig("debug", "")
	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	// Setup file using helpers
	dir, _ := copyTestSrcToTempDir(t)
	chdirTo(t, dir)

	// RelayManager with empty ffmpegLogLevel to trigger default
	rl := NewRelayManager(log, dir, "")
	rl.SetRTSPServer(rtspServer)

	inputName := "testInput"
	outputName := "testOutput"
	inputURL := "file://testsrc.mp4"
	outputURL := "rtmp://localhost/live/test"
	opts := &FFmpegOptions{
		VideoCodec: "libx265",
		AudioCodec: "aac",
		Resolution: "640x360",
		Framerate:  "24",
		Bitrate:    "500k",
		Rotation:   "transpose=2",
		ExtraArgs:  []string{"-preset", "fast", "-tune", "zerolatency"},
	}
	rl.RegisterInputConfig(inputName, inputURL)
	err := rl.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, opts, "")
	if err == nil {
		rl.StopRelay(inputURL, outputURL, inputName, outputName)
	} else {
		t.Fatalf("StartRelayWithOptions with all FFmpegOptions failed: %v", err)
	}
}

func TestStatusV2_MultipleRelays(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	// Simulate multiple input relays
	inURL1 := "rtsp://localhost:8554/test1"
	inURL2 := "rtsp://localhost:8554/test2"
	rl.InputRelays.Relays[inURL1] = &InputRelay{
		InputURL:  inURL1,
		InputName: "in1",
		LocalURL:  "local1",
		Status:    InputRunning,
	}
	rl.InputRelays.Relays[inURL2] = &InputRelay{
		InputURL:  inURL2,
		InputName: "in2",
		LocalURL:  "local2",
		Status:    InputError,
	}
	// Simulate output relays
	outURL1 := "rtmp://localhost/live/test1"
	outURL2 := "rtmp://localhost/live/test2"
	rl.OutputRelays.Relays[outURL1] = &OutputRelay{
		OutputURL:  outURL1,
		OutputName: "out1",
		InputURL:   inURL1,
		LocalURL:   "local1",
		Status:     OutputRunning,
	}
	rl.OutputRelays.Relays[outURL2] = &OutputRelay{
		OutputURL:  outURL2,
		OutputName: "out2",
		InputURL:   inURL2,
		LocalURL:   "local2",
		Status:     OutputError,
	}
	status := rl.StatusV2()
	if len(status.Relays) != 2 {
		t.Errorf("expected 2 input relays, got %d", len(status.Relays))
	}
	foundInputs := map[string]bool{}
	foundOutputs := map[string]bool{}
	for _, s := range status.Relays {
		foundInputs[s.Input.InputURL] = true
		if s.Input.Status == "" {
			t.Errorf("input relay status should not be empty")
		}
		for _, o := range s.Outputs {
			foundOutputs[o.OutputURL] = true
			if o.Status == "" {
				t.Errorf("output relay status should not be empty")
			}
		}
	}
	for _, url := range []string{inURL1, inURL2} {
		if !foundInputs[url] {
			t.Errorf("input relay %s missing from status", url)
		}
	}
	for _, url := range []string{outURL1, outURL2} {
		if !foundOutputs[url] {
			t.Errorf("output relay %s missing from status", url)
		}
	}
}

func TestStopAllRelays_MixedStates(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	// Simulate input relays in different states
	inURL1 := "rtsp://localhost:8554/test1"
	inURL2 := "rtsp://localhost:8554/test2"
	inURL3 := "rtsp://localhost:8554/test3"
	rl.InputRelays.Relays[inURL1] = &InputRelay{
		InputURL:  inURL1,
		InputName: "in1",
		LocalURL:  "local1",
		Status:    InputRunning,
	}
	rl.InputRelays.Relays[inURL2] = &InputRelay{
		InputURL:  inURL2,
		InputName: "in2",
		LocalURL:  "local2",
		Status:    InputStopped,
	}
	rl.InputRelays.Relays[inURL3] = &InputRelay{
		InputURL:  inURL3,
		InputName: "in3",
		LocalURL:  "local3",
		Status:    InputError,
	}
	// Simulate output relays in different states
	outURL1 := "rtmp://localhost/live/test1"
	outURL2 := "rtmp://localhost/live/test2"
	outURL3 := "rtmp://localhost/live/test3"
	rl.OutputRelays.Relays[outURL1] = &OutputRelay{
		OutputURL:  outURL1,
		OutputName: "out1",
		InputURL:   inURL1,
		LocalURL:   "local1",
		Status:     OutputRunning,
	}
	rl.OutputRelays.Relays[outURL2] = &OutputRelay{
		OutputURL:  outURL2,
		OutputName: "out2",
		InputURL:   inURL2,
		LocalURL:   "local2",
		Status:     OutputStopped,
	}
	rl.OutputRelays.Relays[outURL3] = &OutputRelay{
		OutputURL:  outURL3,
		OutputName: "out3",
		InputURL:   inURL3,
		LocalURL:   "local3",
		Status:     OutputError,
	}
	// Call StopAllRelays and verify all relays are stopped
	rl.StopAllRelays()
	for _, r := range rl.InputRelays.Relays {
		if r.Status != InputStopped {
			t.Errorf("input relay %s not stopped, got status %v", r.InputURL, r.Status)
		}
	}
	for _, r := range rl.OutputRelays.Relays {
		if r.Status != OutputStopped {
			t.Errorf("output relay %s not stopped, got status %v", r.OutputURL, r.Status)
		}
	}
}

func TestImportConfig_RelaysCannotStart(t *testing.T) {
	t.Parallel()
	// Setup: export a config with a relay that cannot be started (missing file)
	rl := newTestRelayManager()
	inputName := "testInput"
	outputName := "testOutput"
	inputURL := "file://nonexistent.mp4"
	outputURL := "rtmp://localhost/live/test"
	// Register input config and relays
	rl.RegisterInputConfig(inputName, inputURL)
	rl.InputRelays.Relays[inputURL] = &InputRelay{
		InputURL:  inputURL,
		InputName: inputName,
		LocalURL:  "local",
		Status:    InputRunning,
	}
	rl.OutputRelays.Relays[outputURL] = &OutputRelay{
		OutputURL:  outputURL,
		OutputName: outputName,
		InputURL:   inputURL,
		LocalURL:   "local",
		Status:     OutputRunning,
	}
	file := t.TempDir() + "/test_import_fail.json"
	if err := rl.ExportConfig(file); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	// Clear relays and configs
	rl.InputRelays.Relays = make(map[string]*InputRelay)
	rl.OutputRelays.Relays = make(map[string]*OutputRelay)
	rl.inputConfigs = make(map[string]*InputConfig)
	// Import config: should error due to missing input file
	err := rl.ImportConfig(file)
	if err == nil {
		t.Errorf("expected error for relay startup failure during import")
	}
}

func TestStatusV2_EmptyRelays(t *testing.T) {
	t.Parallel()
	rl := newTestRelayManager()
	status := rl.StatusV2()
	if len(status.Relays) != 0 {
		t.Errorf("expected 0 relays, got %d", len(status.Relays))
	}
}

func TestStopInputRelayForConsumer_Basic(t *testing.T) {
	t.Parallel()
	// Setup file using helpers
	dir, _ := copyTestSrcToTempDir(t)
	chdirTo(t, dir)

	// Setup RTSP server and relay manager with the same directory
	log := logger.NewLoggerWithConfig("debug", "")
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	rl := NewRelayManager(log, dir, "error")
	rl.SetRTSPServer(rtspServer)
	rl.SetTimeouts(5*time.Second, 5*time.Second)

	inputName := "testInput"
	inputURL := "file://testsrc.mp4"
	// Register input config
	rl.RegisterInputConfig(inputName, inputURL)
	// Start input relay for consumer (real logic)
	_, err := rl.StartInputRelayForConsumer(inputName)
	if err != nil {
		t.Fatalf("failed to start input relay: %v", err)
	}
	// Wait for status to become InputRunning
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rl.InputRelays.mu.Lock()
		status := rl.InputRelays.Relays[inputURL].Status
		rl.InputRelays.mu.Unlock()
		if status == InputRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Stop input relay for consumer
	rl.StopInputRelayForConsumer(inputName)
	// Wait for status to become InputStopped
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rl.InputRelays.mu.Lock()
		status := rl.InputRelays.Relays[inputURL].Status
		rl.InputRelays.mu.Unlock()
		if status == InputStopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("input relay not stopped, got status %v", rl.InputRelays.Relays[inputURL].Status)
}
