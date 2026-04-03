package worker

import (
	"testing"

	"go-mls/internal/logger"
	"go-mls/internal/state"

	"github.com/stretchr/testify/assert"
)

func TestRecorder_Struct(t *testing.T) {
	store := state.NewStore()

	rec := &Recorder{
		store:     store,
		recording: &state.Recording{StreamPath: "test"},
	}

	assert.Equal(t, store, rec.store)
	assert.Equal(t, "test", rec.recording.StreamPath)
}

func TestRestreamer_Struct(t *testing.T) {
	store := state.NewStore()
	out := &state.Output{StreamPath: "test", OutputID: "out1"}

	rs := &Restreamer{
		store:  store,
		output: out,
	}

	assert.Equal(t, store, rs.store)
	assert.Equal(t, "test", rs.output.StreamPath)
	assert.Equal(t, "out1", rs.output.OutputID)
}

func TestHLSManager_Struct(t *testing.T) {
	log := logger.NewLogger()
	store := state.NewStore()

	mgr := NewHLSManager(store, log, "/tmp/hls", "ultrafast", 1935)

	assert.Equal(t, store, mgr.store)
	assert.NotNil(t, mgr.log)
	assert.Equal(t, "/tmp/hls", mgr.baseDir)
	assert.Equal(t, "ultrafast", mgr.preset)
	assert.Equal(t, 1935, mgr.rtmpPort)
	assert.NotNil(t, mgr.sessions)
}

func TestHLSManager_Sessions(t *testing.T) {
	log := logger.NewLogger()
	store := state.NewStore()

	mgr := NewHLSManager(store, log, "/tmp/hls", "ultrafast", 1935)

	assert.Equal(t, 0, len(mgr.sessions))
}

func TestHLSManager_Shutdown(t *testing.T) {
	log := logger.NewLogger()
	store := state.NewStore()

	mgr := NewHLSManager(store, log, "/tmp/hls", "ultrafast", 1935)

	mgr.Shutdown()
	assert.Equal(t, 0, len(mgr.sessions))
}

func TestHLSSession_Struct(t *testing.T) {
	sess := &hlsSession{
		viewerCount: 5,
		playlistDir: "/tmp/hls/test",
	}

	assert.Equal(t, 5, sess.viewerCount)
	assert.Equal(t, "/tmp/hls/test", sess.playlistDir)
}

func TestStateRecording(t *testing.T) {
	rec := &state.Recording{
		StreamPath: "live/stream",
		Filename:   "test_20240101.mp4",
		Status:     state.RecordingStatusActive,
		PID:        12345,
	}

	assert.Equal(t, "live/stream", rec.StreamPath)
	assert.Equal(t, "test_20240101.mp4", rec.Filename)
	assert.Equal(t, state.RecordingStatusActive, rec.Status)
	assert.Equal(t, 12345, rec.PID)
}

func TestStateOutput(t *testing.T) {
	out := &state.Output{
		StreamPath: "live/stream",
		OutputID:   "youtube",
		RemoteURL:  "rtmp://youtube.com/live",
		StreamKey:  "secret-key",
		Status:     state.OutputStatusRunning,
		PID:        12345,
	}

	assert.Equal(t, "live/stream", out.StreamPath)
	assert.Equal(t, "youtube", out.OutputID)
	assert.Equal(t, "rtmp://youtube.com/live", out.RemoteURL)
	assert.Equal(t, "secret-key", out.StreamKey)
	assert.Equal(t, state.OutputStatusRunning, out.Status)
}

func TestStateOutput_WithVideoAudioArgs(t *testing.T) {
	out := &state.Output{
		StreamPath: "live/stream",
		OutputID:   "custom",
		RemoteURL:  "rtmp://custom.com/stream",
		VideoArgs:  []string{"-c:v", "libx264", "-preset", "fast"},
		AudioArgs:  []string{"-c:a", "aac", "-b:a", "128k"},
		Status:     state.OutputStatusStarting,
	}

	assert.Len(t, out.VideoArgs, 4)
	assert.Len(t, out.AudioArgs, 4)
	assert.Equal(t, "-c:v", out.VideoArgs[0])
	assert.Equal(t, "-c:a", out.AudioArgs[0])
}

func TestStateHLSSession(t *testing.T) {
	sess := &state.HLSSession{
		StreamPath:  "live/stream",
		PlaylistDir: "/tmp/hls/live/stream",
		ViewerCount: 10,
		PID:         12345,
	}

	assert.Equal(t, "live/stream", sess.StreamPath)
	assert.Equal(t, "/tmp/hls/live/stream", sess.PlaylistDir)
	assert.Equal(t, 10, sess.ViewerCount)
	assert.Equal(t, 12345, sess.PID)
}

func TestStateOutputStatus(t *testing.T) {
	statuses := []state.OutputStatus{
		state.OutputStatusStarting,
		state.OutputStatusRunning,
		state.OutputStatusStopped,
		state.OutputStatusError,
	}

	for _, s := range statuses {
		assert.NotEmpty(t, string(s))
	}
}

func TestStateRecordingStatus(t *testing.T) {
	statuses := []state.RecordingStatus{
		state.RecordingStatusActive,
		state.RecordingStatusStopped,
	}

	for _, s := range statuses {
		assert.NotEmpty(t, string(s))
	}
}

func TestFFmpegArgs_Recording(t *testing.T) {
	localInput := "rtmp://127.0.0.1:1935/test"
	outPath := "/tmp/recordings/test.mp4"

	args := []string{
		"-i", localInput,
		"-c", "copy",
		"-movflags", "+faststart",
		outPath,
	}

	assert.Contains(t, args, "-i")
	assert.Contains(t, args, "-c")
	assert.Contains(t, args, "copy")
	assert.Contains(t, args, "-movflags")
	assert.Contains(t, args, "+faststart")
}

func TestFFmpegArgs_HLS(t *testing.T) {
	localInput := "rtmp://127.0.0.1:1935/test"
	playlistPath := "/tmp/hls/test/index.m3u8"

	args := []string{
		"-i", localInput,
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+append_list",
		playlistPath,
	}

	assert.Contains(t, args, "-i")
	assert.Contains(t, args, "-c:v")
	assert.Contains(t, args, "libx264")
	assert.Contains(t, args, "-preset")
	assert.Contains(t, args, "ultrafast")
	assert.Contains(t, args, "-f")
	assert.Contains(t, args, "hls")
	assert.Contains(t, args, "-hls_time")
	assert.Contains(t, args, "2")
	assert.Contains(t, args, "-hls_list_size")
	assert.Contains(t, args, "6")
}

func TestFFmpegArgs_Restreamer(t *testing.T) {
	localInput := "rtmp://127.0.0.1:1935/test"
	remoteURL := "rtmp://youtube.com/live/stream-key"

	args := []string{"-i", localInput, "-c:v", "copy", "-c:a", "copy", "-f", "flv", remoteURL}

	assert.Contains(t, args, "-i")
	assert.Contains(t, args, localInput)
	assert.Contains(t, args, "-c:v")
	assert.Contains(t, args, "copy")
	assert.Contains(t, args, "-c:a")
	assert.Contains(t, args, "-f")
	assert.Contains(t, args, "flv")
	assert.Contains(t, args, remoteURL)
}
