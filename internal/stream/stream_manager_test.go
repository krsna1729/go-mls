package stream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-mls/internal/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamManager_StartStopStream(t *testing.T) {
	log := logger.NewLogger()
	tmpDir := t.TempDir()
	sm := NewStreamManager(log, tmpDir, "info")
	sm.SetTimeouts(100*time.Millisecond, 100*time.Millisecond)

	// Mock RTSP server
	rtspServer := NewRTSPServerManager(log, "localhost", 8554)
	sm.SetRTSPServer(rtspServer)

	// Mock StreamProvider behavior by pre-registering input if needed,
	// but StartStream handles registration.

	// We need to mock the actual stream availability or expect failure if no stream.
	// Since we don't have a real stream, we expect StartStream to fail or we need to mock InputRelayManager's GetStream.
	// InputRelayManager uses RTSP server.

	// For this unit test, we can verify that StartStream calls InputRelays.GetStream.
	// However, without a real stream, it will fail.
	// Let's test the error case which confirms the flow.

	err := sm.StartStream("rtsp://fake/stream", "rtmp://fake/out", "test_in", "test_out", nil, "")
	// It should fail because input stream is not available
	assert.Error(t, err)
}

func TestStreamManager_Status(t *testing.T) {
	log := logger.NewLogger()
	tmpDir := t.TempDir()
	sm := NewStreamManager(log, tmpDir, "info")

	status := sm.Status()
	assert.NotNil(t, status)
	assert.Empty(t, status.Relays)
	assert.Empty(t, status.Recordings)
	assert.Empty(t, status.HLS)
}

func TestStreamManager_ConfigExportImport(t *testing.T) {
	log := logger.NewLogger()
	tmpDir := t.TempDir()
	sm := NewStreamManager(log, tmpDir, "info")

	configFile := filepath.Join(tmpDir, "config.json")

	// Export empty config
	err := sm.ExportConfig(configFile)
	require.NoError(t, err)

	// Import it back
	err = sm.ImportConfig(configFile)
	require.NoError(t, err)

	// Create a dummy config file
	configData := `[
		{
			"input_url": "rtsp://test/1",
			"input_name": "test1",
			"outputs": [
				{
					"output_url": "rtmp://out/1",
					"output_name": "out1",
					"platform_preset": "YouTube"
				}
			]
		}
	]`
	err = os.WriteFile(configFile, []byte(configData), 0644)
	require.NoError(t, err)

	// Import should try to start relays (errors are logged but not returned by ImportConfig)
	err = sm.ImportConfig(configFile)
	assert.NoError(t, err)
}

func TestStreamManager_DeleteInput(t *testing.T) {
	log := logger.NewLogger()
	tmpDir := t.TempDir()
	sm := NewStreamManager(log, tmpDir, "info")

	// Register an input
	inputURL := "rtsp://test/in"
	inputName := "test_in"
	sm.InputRelays.RegisterInputConfig(inputName, inputURL)

	// Manually inject a dummy relay into the map so DeleteInput finds it
	// We need to use internal access or a helper if possible, but for this test package it's fine
	// since we are in the same package 'stream'.
	// However, InputRelay struct fields are exported, so we can construct one.
	// We need to lock the map to be safe, though in this test it's single threaded.
	// Note: We can't access sm.InputRelays.Relays directly if it's protected or if we want to be clean.
	// But looking at InputRelayManager, Relays is exported.

	// We need to use a helper or just force it.
	// Actually, let's just use StartInputRelay with a mock if possible? No, that tries to run ffmpeg.
	// We will inject directly.
	sm.InputRelays.Relays[inputURL] = &InputRelay{
		InputURL:  inputURL,
		InputName: inputName,
		Status:    InputStopped,
	}

	// Delete it
	err := sm.DeleteInput(inputURL, inputName)
	assert.NoError(t, err)

	// Verify it's gone
	_, exists := sm.InputRelays.Relays[inputURL]
	assert.False(t, exists)
}
