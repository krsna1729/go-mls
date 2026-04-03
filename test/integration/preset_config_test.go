package integration_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/stream"

	"github.com/stretchr/testify/require"
)

func TestPresetConfigImport(t *testing.T) {
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s not found", testFile)
	}

	tempDir := t.TempDir()
	log := logger.NewLogger()

	srcData, err := os.ReadFile(testFile)
	require.NoError(t, err, "Failed to read test file")

	tamilInputFile := filepath.Join(tempDir, "tamil_input.mp4")
	englishInputFile := filepath.Join(tempDir, "english_input.mp4")
	err = os.WriteFile(tamilInputFile, srcData, 0644)
	require.NoError(t, err)
	err = os.WriteFile(englishInputFile, srcData, 0644)
	require.NoError(t, err)

	rtspServer := stream.NewRTSPServerManager(log, "127.0.0.1", 0)
	err = rtspServer.Start()
	require.NoError(t, err)
	t.Cleanup(func() { rtspServer.Stop() })

	pipeline := stream.NewPipeline(log, tempDir, 60*time.Second)
	pipeline.SetRTSPServer(rtspServer)
	t.Cleanup(func() { pipeline.Shutdown() })

	presets := map[string]stream.FFmpegOpts{
		"YouTube": {
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "30",
			Bitrate:    "4500k",
		},
		"Instagram": {
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "720x1280",
			Framerate:  "30",
			Bitrate:    "3500k",
		},
	}

	require.NotNil(t, presets["YouTube"])
	require.NotNil(t, presets["Instagram"])

	require.Equal(t, "libx264", presets["YouTube"].VideoCodec)
	require.Equal(t, "1920x1080", presets["YouTube"].Resolution)
	require.Equal(t, "4500k", presets["YouTube"].Bitrate)

	require.Equal(t, "720x1280", presets["Instagram"].Resolution)

	opts := stream.ApplyPresetAndOptions("YouTube", nil)
	require.Equal(t, "libx264", opts.VideoCodec)
	require.Equal(t, "1920x1080", opts.Resolution)

	opts2 := stream.ApplyPresetAndOptions("YouTube", map[string]string{
		"resolution": "1280x720",
	})
	require.Equal(t, "1280x720", opts2.Resolution)

	t.Log("Preset and option application verified")
}
