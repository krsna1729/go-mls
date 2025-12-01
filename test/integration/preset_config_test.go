package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/stream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPresetConfigImport verifies that platform presets and manual options
// are correctly applied when importing relay configurations
func TestPresetConfigImport(t *testing.T) {
	// Skip if test file doesn't exist
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s not found", testFile)
	}

	// Setup
	tempDir := t.TempDir()
	log := logger.NewLogger()

	// Copy test file to tempDir for inputs
	srcData, err := os.ReadFile(testFile)
	require.NoError(t, err, "Failed to read test file")

	tamilInputFile := filepath.Join(tempDir, "tamil_input.mp4")
	englishInputFile := filepath.Join(tempDir, "english_input.mp4")
	err = os.WriteFile(tamilInputFile, srcData, 0644)
	require.NoError(t, err)
	err = os.WriteFile(englishInputFile, srcData, 0644)
	require.NoError(t, err)

	// Create RTSP server
	rtspServer := stream.NewRTSPServerManager(log, "127.0.0.1", 0)
	err = rtspServer.Start()
	require.NoError(t, err)
	t.Cleanup(func() { rtspServer.Stop() })

	// Create stream manager
	streamMgr := stream.NewStreamManager(log, tempDir, "error")
	streamMgr.SetRTSPServer(rtspServer)

	// Define test config with various preset/option combinations using file:// URLs
	config := []map[string]interface{}{
		{
			"input_url":  "file://tamil_input.mp4",
			"input_name": "Tamil",
			"outputs": []map[string]interface{}{
				{
					"output_url":      "file://tamil_output_ig.flv",
					"output_name":     "TN-2",
					"platform_preset": "Instagram",
				},
				{
					"output_url":  "file://tamil_output_custom.flv",
					"output_name": "TN-1",
					"ffmpeg_options": map[string]string{
						"video_codec": "",
						"audio_codec": "aac",
						"resolution":  "1280x720",
						"framerate":   "60",
						"bitrate":     "2000k",
						"rotation":    "",
					},
				},
			},
		},
		{
			"input_url":  "file://english_input.mp4",
			"input_name": "English",
			"outputs": []map[string]interface{}{
				{
					"output_url":      "file://english_output_yt.flv",
					"output_name":     "ENG-1",
					"platform_preset": "YouTube",
					"ffmpeg_options": map[string]string{
						"video_codec": "libx264",
						"audio_codec": "aac",
						"resolution":  "1920x1080",
						"framerate":   "30",
						"bitrate":     "4500k",
						"rotation":    "transpose=2",
					},
				},
				{
					"output_url":  "file://english_output_none.flv",
					"output_name": "ENG-2",
				},
			},
		},
	}

	// Write config to file
	configFile := filepath.Join(tempDir, "test_relay_config.json")
	configData, err := json.MarshalIndent(config, "", "  ")
	require.NoError(t, err)
	err = os.WriteFile(configFile, configData, 0644)
	require.NoError(t, err)

	// Import config (this will fail to connect to RTMP servers, but we just want to check the args)
	err = streamMgr.ImportConfig(configFile)
	// We expect errors because RTMP servers don't exist, but we can still check the args
	t.Logf("Import completed (errors expected): %v", err)

	// Give processes a moment to start
	time.Sleep(500 * time.Millisecond)

	// Verify FFmpeg arguments for each output
	testCases := []struct {
		outputURL     string
		description   string
		expectedArgs  []string // Args that MUST be present
		forbiddenArgs []string // Args that should NOT be present
	}{
		{
			outputURL:   "file://tamil_output_ig.flv",
			description: "TN-2 (Instagram preset only)",
			expectedArgs: []string{
				"-c:v", "libx264",
				"-c:a", "aac",
				"-s", "720x1280",
				"-r", "30",
				"-b:v", "3500k",
				"-vf", "transpose=1",
			},
			forbiddenArgs: nil,
		},
		{
			outputURL:   "file://tamil_output_custom.flv",
			description: "TN-1 (manual options, empty strings skipped)",
			expectedArgs: []string{
				"-c:a", "aac",
				"-s", "1280x720",
				"-r", "60",
				"-b:v", "2000k",
			},
			forbiddenArgs: []string{
				"-c:v", // video_codec was empty
				"-vf",  // rotation was empty
			},
		},
		{
			outputURL:   "file://english_output_yt.flv",
			description: "ENG-1 (YouTube preset + manual overrides)",
			expectedArgs: []string{
				"-c:v", "libx264",
				"-c:a", "aac",
				"-s", "1920x1080",
				"-r", "30",
				"-b:v", "4500k",
				"-vf", "transpose=2", // Manual override should win
			},
			forbiddenArgs: nil,
		},
		{
			outputURL:   "file://english_output_none.flv",
			description: "ENG-2 (no preset, no options)",
			expectedArgs: []string{
				"-f", "flv",
			},
			forbiddenArgs: []string{
				"-c:v", "-c:a", "-s", "-r", "-b:v", "-vf",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			// Get the output relay using helper
			outputRelay, exists := streamMgr.OutputRelays.GetOutputRelay(tc.outputURL)

			require.True(t, exists, "Output relay should exist for %s", tc.outputURL)
			require.NotNil(t, outputRelay, "Output relay should not be nil")

			// Get FFmpeg args
			argsStr := strings.Join(outputRelay.FFmpegArgs, " ")
			t.Logf("FFmpeg args for %s: %s", tc.description, argsStr)

			// Check expected args
			for i := 0; i < len(tc.expectedArgs); i += 2 {
				if i+1 < len(tc.expectedArgs) {
					flag := tc.expectedArgs[i]
					value := tc.expectedArgs[i+1]
					assert.Contains(t, outputRelay.FFmpegArgs, flag,
						"Expected flag %s in args for %s", flag, tc.description)
					// Find the flag and check the next arg is the value
					for j, arg := range outputRelay.FFmpegArgs {
						if arg == flag && j+1 < len(outputRelay.FFmpegArgs) {
							assert.Equal(t, value, outputRelay.FFmpegArgs[j+1],
								"Expected %s %s for %s", flag, value, tc.description)
							break
						}
					}
				}
			}

			// Check forbidden args
			for _, forbiddenFlag := range tc.forbiddenArgs {
				assert.NotContains(t, outputRelay.FFmpegArgs, forbiddenFlag,
					"Should not contain %s in args for %s", forbiddenFlag, tc.description)
			}
		})
	}

	t.Log("✅ All preset and manual option combinations verified successfully")
}
