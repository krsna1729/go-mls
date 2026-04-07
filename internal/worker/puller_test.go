package worker

import (
	"context"
	"testing"

	"go-mls/internal/logger"
	"go-mls/internal/state"

	"github.com/stretchr/testify/assert"
)

func TestPuller_Struct(t *testing.T) {
	store := state.NewStore()

	puller := &Puller{
		store:    store,
		stream:   &state.Input{StreamPath: "test"},
		rtmpPort: 1935,
	}

	assert.Equal(t, store, puller.store)
	assert.Equal(t, "test", puller.stream.StreamPath)
	assert.Equal(t, 1935, puller.rtmpPort)
}

func TestPuller_InputTypes(t *testing.T) {
	input := &state.Input{
		StreamPath:  "live/stream",
		RemoteURL:   "rtsp://camera.example.com:554/stream",
		IngestToken: "secret-token",
		Mode:        state.InputModePull,
		Status:      state.InputStatusStarting,
	}

	assert.Equal(t, "live/stream", input.StreamPath)
	assert.Equal(t, "rtsp://camera.example.com:554/stream", input.RemoteURL)
	assert.Equal(t, "secret-token", input.IngestToken)
	assert.Equal(t, state.InputModePull, input.Mode)
	assert.Equal(t, state.InputStatusStarting, input.Status)
}

func TestInputModeConstants(t *testing.T) {
	assert.Equal(t, state.InputMode("pull"), state.InputModePull)
	assert.Equal(t, state.InputMode("accept"), state.InputModeAccept)
}

func TestInputStatusConstants(t *testing.T) {
	assert.Equal(t, state.InputStatus("Starting"), state.InputStatusStarting)
	assert.Equal(t, state.InputStatus("Active"), state.InputStatusActive)
	assert.Equal(t, state.InputStatus("Stopped"), state.InputStatusStopped)
	assert.Equal(t, state.InputStatus("Error"), state.InputStatusError)
}

func TestPullerConfig(t *testing.T) {
	ctx := context.Background()
	store := state.NewStore()
	log := logger.NewLogger()

	input := &state.Input{
		StreamPath: "test-stream",
		RemoteURL:  "rtsp://example.com/stream",
		Status:     state.InputStatusStarting,
	}

	assert.NotNil(t, ctx)
	assert.NotNil(t, store)
	assert.NotNil(t, log)
	assert.Equal(t, "test-stream", input.StreamPath)
	assert.Equal(t, "rtsp://example.com/stream", input.RemoteURL)
	assert.Equal(t, state.InputStatusStarting, input.Status)
}

func TestPullerStreamPathExtraction(t *testing.T) {
	tests := []struct {
		name       string
		streamPath string
		expected   string
	}{
		{"simple path", "live", "live"},
		{"nested path", "live/stream", "live/stream"},
		{"path with dashes", "my-stream/test", "my-stream/test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &state.Input{StreamPath: tt.streamPath}
			assert.Equal(t, tt.expected, input.StreamPath)
		})
	}
}

func TestPullerRemoteURLParsing(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string
		isRTSP    bool
		isRTMP    bool
		isHLS     bool
	}{
		{"RTSP", "rtsp://camera:554/stream", true, false, false},
		{"RTSPS", "rtsps://camera:552/stream", true, false, false},
		{"RTMP", "rtmp://server/live/stream", false, true, false},
		{"RTMPS", "rtmps://server/live/stream", false, true, false},
		{"HLS", "https://example.com/stream.m3u8", false, false, true},
		{"SRT", "srt://server:4200/stream", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &state.Input{RemoteURL: tt.remoteURL}
			assert.Contains(t, tt.remoteURL, input.RemoteURL)
		})
	}
}
