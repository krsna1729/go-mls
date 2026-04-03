package stream

import (
	"testing"
	"time"

	"go-mls/internal/logger"

	"github.com/stretchr/testify/assert"
)

func TestNewPipeline(t *testing.T) {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp", 60*time.Second)
	assert.NotNil(t, p)
	assert.NotNil(t, p.Logger)
	assert.NotNil(t, p.SSE)
	assert.NotNil(t, p.relays)
	assert.Equal(t, "/tmp", p.recDir)
}

func TestPipeline_SetRTSPServer(t *testing.T) {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp", 60*time.Second)
	assert.NotNil(t, p)

	rm := NewRTSPServerManager(log, "127.0.0.1", 8554)
	p.SetRTSPServer(rm)
	assert.NotNil(t, p.RTSPSrv)
}

func TestPipeline_SetFFmpegFactory(t *testing.T) {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp", 60*time.Second)
	assert.NotNil(t, p)

	factory := NewTestFFmpegFactory()
	p.SetFFmpegFactory(factory)
	assert.NotNil(t, p.FFmpeg)
}

func TestPipeline_GetRecDir(t *testing.T) {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp/recordings", 60*time.Second)
	assert.NotNil(t, p)

	assert.Equal(t, "/tmp/recordings", p.GetRecDir())
}

func TestPipeline_resolveInputURL(t *testing.T) {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp", 60*time.Second)

	tests := []struct {
		name    string
		input   string
		wantURL string
		wantErr bool
	}{
		{"rtsp URL unchanged", "rtsp://localhost:8554/stream", "rtsp://localhost:8554/stream", false},
		{"file URL with missing file returns error", "file://nonexistent.mp4", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := p.resolveInputURL(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantURL, url)
			}
		})
	}
}

func TestPipeline_resolveOutputURL(t *testing.T) {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp", 60*time.Second)

	tests := []struct {
		name    string
		input   string
		wantURL string
		wantErr bool
	}{
		{"rtmp URL unchanged", "rtmp://localhost/live/stream", "rtmp://localhost/live/stream", false},
		{"file URL resolves to path", "file://output.mp4", "/tmp/output.mp4", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := p.resolveOutputURL(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantURL, url)
			}
		})
	}
}

func TestPipelineStream_RefCount(t *testing.T) {
	stream := &PipelineStream{
		Name: "test",
		Type: PTypeInput,
	}

	assert.Equal(t, 0, stream.RefCount)
	stream.IncrementRef()
	assert.Equal(t, 1, stream.RefCount)
	stream.IncrementRef()
	assert.Equal(t, 2, stream.RefCount)
	stream.DecrementRef()
	assert.Equal(t, 1, stream.RefCount)
	stream.DecrementRef()
	assert.Equal(t, 0, stream.RefCount)
}

func TestPipelineStream_CanStop(t *testing.T) {
	stream := &PipelineStream{
		Name: "test",
		Type: PTypeInput,
	}

	assert.True(t, stream.CanStop())
	stream.IncrementRef()
	assert.False(t, stream.CanStop())
	stream.DecrementRef()
	assert.True(t, stream.CanStop())
}

func TestPipelineStreamStatus_String(t *testing.T) {
	assert.Equal(t, "Stopped", PStreamStopped.String())
	assert.Equal(t, "Starting", PStreamStarting.String())
	assert.Equal(t, "Running", PStreamRunning.String())
	assert.Equal(t, "Error", PStreamError.String())
}

func TestPipelineStreamType(t *testing.T) {
	assert.Equal(t, "input", string(PTypeInput))
	assert.Equal(t, "output", string(PTypeOutput))
	assert.Equal(t, "hls", string(PTypeHLS))
}

func TestApplyPresetAndOptions(t *testing.T) {
	tests := []struct {
		name        string
		preset      string
		options     map[string]string
		expectCodec string
		expectRes   string
	}{
		{
			name:        "YouTube preset",
			preset:      "YouTube",
			options:     nil,
			expectCodec: "libx264",
			expectRes:   "1920x1080",
		},
		{
			name:        "YouTube with override",
			preset:      "YouTube",
			options:     map[string]string{"resolution": "1280x720"},
			expectCodec: "libx264",
			expectRes:   "1280x720",
		},
		{
			name:        "Custom preset",
			preset:      "Custom",
			options:     map[string]string{"video_codec": "libvpx"},
			expectCodec: "libvpx",
			expectRes:   "",
		},
		{
			name:        "Unknown preset",
			preset:      "Unknown",
			options:     nil,
			expectCodec: "",
			expectRes:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ApplyPresetAndOptions(tt.preset, tt.options)
			assert.Equal(t, tt.expectCodec, opts.VideoCodec)
			assert.Equal(t, tt.expectRes, opts.Resolution)
		})
	}
}

func TestParsePreset(t *testing.T) {
	tests := []struct {
		name     string
		preset   string
		expected FFmpegOpts
	}{
		{
			name:   "YouTube",
			preset: "YouTube",
			expected: FFmpegOpts{
				VideoCodec: "libx264",
				AudioCodec: "aac",
				Resolution: "1920x1080",
				Framerate:  "30",
				Bitrate:    "4500k",
			},
		},
		{
			name:   "Twitch",
			preset: "Twitch",
			expected: FFmpegOpts{
				VideoCodec: "libx264",
				AudioCodec: "aac",
				Resolution: "1920x1080",
				Framerate:  "30",
				Bitrate:    "4500k",
			},
		},
		{
			name:     "Unknown",
			preset:   "Unknown",
			expected: FFmpegOpts{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ParsePreset(tt.preset)
			assert.Equal(t, tt.expected.VideoCodec, opts.VideoCodec)
			assert.Equal(t, tt.expected.AudioCodec, opts.AudioCodec)
			assert.Equal(t, tt.expected.Resolution, opts.Resolution)
		})
	}
}

func TestFFmpegOptsFromMap(t *testing.T) {
	m := map[string]string{
		"video_codec": "libx264",
		"audio_codec": "aac",
		"resolution":  "1920x1080",
		"framerate":   "30",
		"bitrate":     "4500k",
	}

	opts := FFmpegOptsFromMap(m)
	assert.Equal(t, "libx264", opts.VideoCodec)
	assert.Equal(t, "aac", opts.AudioCodec)
	assert.Equal(t, "1920x1080", opts.Resolution)
	assert.Equal(t, "30", opts.Framerate)
	assert.Equal(t, "4500k", opts.Bitrate)
}

func TestFFmpegOptsToMap(t *testing.T) {
	opts := FFmpegOpts{
		VideoCodec: "libx264",
		AudioCodec: "aac",
		Resolution: "1920x1080",
		Framerate:  "30",
		Bitrate:    "4500k",
		ExtraArgs:  []string{"-extra"},
	}

	m := FFmpegOptsToMap(opts)
	assert.Equal(t, "libx264", m["video_codec"])
	assert.Equal(t, "aac", m["audio_codec"])
	assert.Equal(t, "1920x1080", m["resolution"])
	assert.Equal(t, "30", m["framerate"])
	assert.Equal(t, "4500k", m["bitrate"])
}

func TestRelayInputStatus(t *testing.T) {
	status := RelayInputStatus{
		InputName:       "test-input",
		InputURL:        "rtsp://localhost/stream",
		LocalURL:        "rtsp://localhost:8554/relay/test-input",
		Status:          "running",
		LastError:       "",
		RefCount:        2,
		Speed:           1.0,
		CPU:             5.0,
		Mem:             1000000,
		RecordingActive: true,
	}

	assert.Equal(t, "test-input", status.InputName)
	assert.Equal(t, "rtsp://localhost/stream", status.InputURL)
	assert.Equal(t, "running", status.Status)
	assert.Equal(t, 2, status.RefCount)
	assert.True(t, status.RecordingActive)
}

func TestRelayOutputStatus(t *testing.T) {
	status := RelayOutputStatus{
		OutputName: "test-output",
		Status:     "running",
		LastError:  "",
		Preset:     "YouTube",
		Bitrate:    4500.0,
		CPU:        10.0,
		Mem:        2000000,
	}

	assert.Equal(t, "test-output", status.OutputName)
	assert.Equal(t, "running", status.Status)
	assert.Equal(t, "YouTube", status.Preset)
	assert.Equal(t, 4500.0, status.Bitrate)
}

func TestStatusResponse(t *testing.T) {
	resp := StatusResponse{
		Server: PipelineServerStatus{
			CPU: 25.5,
			Mem: 100000000,
		},
		Relays: []RelayStatus{
			{
				Input: RelayInputStatus{
					InputName: "test",
					Status:    "running",
				},
				Outputs: []RelayOutputStatus{
					{
						OutputName: "out1",
						Status:     "running",
					},
				},
			},
		},
	}

	assert.Equal(t, 25.5, resp.Server.CPU)
	assert.Len(t, resp.Relays, 1)
	assert.Equal(t, "test", resp.Relays[0].Input.InputName)
	assert.Len(t, resp.Relays[0].Outputs, 1)
}

func TestRecordingListItem(t *testing.T) {
	item := RecordingListItem{
		Name:      "test-recording",
		Source:    "rtsp://localhost/stream",
		Filename:  "test-recording_1234567890.mp4",
		StartedAt: time.Now(),
		FileSize:  1024000,
		Active:    false,
	}

	assert.Equal(t, "test-recording", item.Name)
	assert.Equal(t, "test-recording_1234567890.mp4", item.Filename)
	assert.Equal(t, int64(1024000), item.FileSize)
	assert.False(t, item.Active)
}

func TestRelay(t *testing.T) {
	relay := &Relay{
		Input: &PipelineStream{
			Name:      "test-input",
			Type:      PTypeInput,
			SourceURL: "rtsp://localhost/stream",
		},
		Outputs: map[string]*PipelineStream{
			"output1": {
				Name: "output1",
				Type: PTypeOutput,
			},
		},
		Recording: &PipelineRecording{
			Name:     "test-recording",
			Filename: "test.mp4",
		},
		HLSSession: &PipelineHLSSession{
			PipelineStream: PipelineStream{
				Name: "hls-session",
				Type: PTypeInput,
			},
			Ready: true,
		},
	}

	assert.NotNil(t, relay.Input)
	assert.Equal(t, "test-input", relay.Input.Name)
	assert.Len(t, relay.Outputs, 1)
	assert.NotNil(t, relay.Recording)
	assert.NotNil(t, relay.HLSSession)
	assert.True(t, relay.HLSSession.Ready)
}

func TestPipelineHLSSession_ViewerManagement(t *testing.T) {
	sess := &PipelineHLSSession{
		PipelineStream: PipelineStream{
			Name:      "test-hls",
			Type:      PTypeHLS,
			SourceURL: "rtsp://localhost/stream",
		},
		Dir:        "/tmp/hls/test-hls",
		Ready:      true,
		ViewerIDs:  map[string]time.Time{},
		LastAccess: time.Now(),
	}

	assert.Equal(t, "test-hls", sess.Name)
	assert.Equal(t, "/tmp/hls/test-hls", sess.Dir)
	assert.True(t, sess.Ready)
	assert.NotNil(t, sess.ViewerIDs)
	assert.Equal(t, 0, len(sess.ViewerIDs))

	sess.Mu.Lock()
	sess.ViewerIDs["viewer-123"] = time.Now()
	sess.Mu.Unlock()
	assert.Equal(t, 1, len(sess.ViewerIDs))

	sess.Mu.Lock()
	delete(sess.ViewerIDs, "viewer-123")
	sess.Mu.Unlock()
	assert.Equal(t, 0, len(sess.ViewerIDs))
}

func TestPipelineRecording(t *testing.T) {
	rec := &PipelineRecording{
		Name:      "test-recording",
		SourceURL: "rtsp://localhost/stream",
		Filename:  "test_1234567890.mp4",
		FilePath:  "/tmp/recordings/test_1234567890.mp4",
		FileSize:  0,
		StartedAt: time.Now(),
		Active:    true,
	}

	assert.Equal(t, "test-recording", rec.Name)
	assert.True(t, rec.Active)

	rec.Active = false
	rec.StoppedAt = time.Now()
	rec.FileSize = 1024000

	assert.False(t, rec.Active)
	assert.Equal(t, int64(1024000), rec.FileSize)
	assert.False(t, rec.StoppedAt.IsZero())
}

func TestPipelineServerStatus(t *testing.T) {
	status := PipelineServerStatus{
		CPU: 50.5,
		Mem: 2147483648,
	}

	assert.Equal(t, 50.5, status.CPU)
	assert.Equal(t, uint64(2147483648), status.Mem)
}

func TestNewPipelineStream(t *testing.T) {
	stream := NewPipelineStream("test-stream", PTypeInput, "rtsp://localhost/stream")

	assert.Equal(t, "test-stream", stream.Name)
	assert.Equal(t, PTypeInput, stream.Type)
	assert.Equal(t, "rtsp://localhost/stream", stream.SourceURL)
	assert.Equal(t, PStreamStopped, stream.Status)
	assert.Equal(t, 0, stream.RefCount)
	assert.True(t, stream.CreatedAt.IsZero() == false)
}

func TestNewPipelineHLSSession(t *testing.T) {
	sess := NewPipelineHLSSession("hls-session", "rtsp://localhost/stream")

	assert.Equal(t, "hls-session", sess.Name)
	assert.Equal(t, PTypeHLS, sess.Type)
	assert.False(t, sess.Ready)
	assert.Equal(t, 0, len(sess.ViewerIDs))
	assert.NotNil(t, sess.ViewerIDs)
}

func TestTestFFmpegFactory(t *testing.T) {
	factory := NewTestFFmpegFactory()
	assert.NotNil(t, factory)
	assert.NotNil(t, factory.procs)
}
