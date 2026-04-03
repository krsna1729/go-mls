package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewStore(t *testing.T) {
	s := NewStore()
	assert.NotNil(t, s.inputs)
	assert.NotNil(t, s.outputs)
	assert.NotNil(t, s.recordings)
	assert.NotNil(t, s.hls)
	assert.NotNil(t, s.telemetry)
	assert.Equal(t, 0, len(s.inputs))
	assert.Equal(t, 0, len(s.outputs))
}

func TestStore_AddInput(t *testing.T) {
	s := NewStore()

	in := &Input{
		StreamPath: "live/stream",
		RemoteURL:  "rtsp://camera:554/stream",
		Mode:       InputModePull,
		Status:     InputStatusStarting,
	}

	err := s.AddInput(in)
	assert.NoError(t, err)

	got, ok := s.GetInput("live/stream")
	assert.True(t, ok)
	assert.Equal(t, "live/stream", got.StreamPath)
	assert.Equal(t, "rtsp://camera:554/stream", got.RemoteURL)
}

func TestStore_AddInput_Duplicate(t *testing.T) {
	s := NewStore()

	in := &Input{StreamPath: "live/stream"}
	err := s.AddInput(in)
	assert.NoError(t, err)

	err = s.AddInput(in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestStore_RemoveInput(t *testing.T) {
	s := NewStore()

	s.AddInput(&Input{StreamPath: "live/stream"})

	err := s.RemoveInput("live/stream")
	assert.NoError(t, err)

	_, ok := s.GetInput("live/stream")
	assert.False(t, ok)
}

func TestStore_RemoveInput_NotFound(t *testing.T) {
	s := NewStore()

	err := s.RemoveInput("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStore_GetInput(t *testing.T) {
	s := NewStore()

	_, ok := s.GetInput("live/stream")
	assert.False(t, ok)

	s.AddInput(&Input{StreamPath: "live/stream", Mode: InputModeAccept})

	in, ok := s.GetInput("live/stream")
	assert.True(t, ok)
	assert.Equal(t, InputModeAccept, in.Mode)
}

func TestStore_ListInputs(t *testing.T) {
	s := NewStore()

	inputs := s.ListInputs()
	assert.Empty(t, inputs)

	s.AddInput(&Input{StreamPath: "stream1"})
	s.AddInput(&Input{StreamPath: "stream2"})

	inputs = s.ListInputs()
	assert.Len(t, inputs, 2)
}

func TestStore_UpdateInputStatus(t *testing.T) {
	s := NewStore()
	s.AddInput(&Input{StreamPath: "live/stream", Status: InputStatusStarting})

	s.UpdateInputStatus("live/stream", InputStatusActive, "")

	in, _ := s.GetInput("live/stream")
	assert.Equal(t, InputStatusActive, in.Status)
	assert.Empty(t, in.LastError)
}

func TestStore_UpdateInputStatus_WithError(t *testing.T) {
	s := NewStore()
	s.AddInput(&Input{StreamPath: "live/stream", Status: InputStatusStarting})

	s.UpdateInputStatus("live/stream", InputStatusError, "connection refused")

	in, _ := s.GetInput("live/stream")
	assert.Equal(t, InputStatusError, in.Status)
	assert.Equal(t, "connection refused", in.LastError)
}

func TestStore_AddOutput(t *testing.T) {
	s := NewStore()

	out := &Output{
		StreamPath: "live/stream",
		OutputID:   "youtube",
		RemoteURL:  "rtmp://youtube.com/live/key",
		Status:     OutputStatusStarting,
	}

	err := s.AddOutput(out)
	assert.NoError(t, err)

	got, ok := s.GetOutput("live/stream", "youtube")
	assert.True(t, ok)
	assert.Equal(t, "youtube", got.OutputID)
}

func TestStore_AddOutput_Duplicate(t *testing.T) {
	s := NewStore()

	s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "youtube"})

	err := s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "youtube"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestStore_RemoveOutput(t *testing.T) {
	s := NewStore()
	s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "youtube"})

	err := s.RemoveOutput("live/stream", "youtube")
	assert.NoError(t, err)

	_, ok := s.GetOutput("live/stream", "youtube")
	assert.False(t, ok)
}

func TestStore_ListOutputsForInput(t *testing.T) {
	s := NewStore()
	s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "youtube"})
	s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "twitch"})
	s.AddOutput(&Output{StreamPath: "other/stream", OutputID: "youtube"})

	outputs := s.ListOutputsForInput("live/stream")
	assert.Len(t, outputs, 2)
}

func TestStore_UpdateOutputStatus(t *testing.T) {
	s := NewStore()
	s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "youtube", Status: OutputStatusStarting})

	s.UpdateOutputStatus("live/stream", "youtube", OutputStatusRunning, "")

	out, _ := s.GetOutput("live/stream", "youtube")
	assert.Equal(t, OutputStatusRunning, out.Status)
}

func TestStore_AddRecording(t *testing.T) {
	s := NewStore()

	rec := &Recording{
		StreamPath: "live/stream",
		Filename:   "live_20240101.mp4",
		StartedAt:  time.Now(),
		Status:     RecordingStatusActive,
	}

	err := s.AddRecording(rec)
	assert.NoError(t, err)

	got, ok := s.GetRecording("live/stream")
	assert.True(t, ok)
	assert.Equal(t, "live_20240101.mp4", got.Filename)
}

func TestStore_AddRecording_Duplicate(t *testing.T) {
	s := NewStore()
	s.AddRecording(&Recording{StreamPath: "live/stream", Filename: "test.mp4"})

	err := s.AddRecording(&Recording{StreamPath: "live/stream", Filename: "test2.mp4"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already active")
}

func TestStore_RemoveRecording(t *testing.T) {
	s := NewStore()
	s.AddRecording(&Recording{StreamPath: "live/stream", Filename: "test.mp4"})

	err := s.RemoveRecording("live/stream")
	assert.NoError(t, err)

	_, ok := s.GetRecording("live/stream")
	assert.False(t, ok)
}

func TestStore_ListRecordings(t *testing.T) {
	s := NewStore()

	recs := s.ListRecordings()
	assert.Empty(t, recs)

	s.AddRecording(&Recording{StreamPath: "stream1", Filename: "s1.mp4"})
	s.AddRecording(&Recording{StreamPath: "stream2", Filename: "s2.mp4"})

	recs = s.ListRecordings()
	assert.Len(t, recs, 2)
}

func TestStore_AddHLSSession(t *testing.T) {
	s := NewStore()

	sess := &HLSSession{
		StreamPath:  "live/stream",
		PlaylistDir: "/tmp/hls/live/stream",
		ViewerCount: 5,
		PID:         12345,
	}

	s.AddHLSSession(sess)

	got, ok := s.GetHLSSession("live/stream")
	assert.True(t, ok)
	assert.Equal(t, 5, got.ViewerCount)
	assert.Equal(t, 12345, got.PID)
}

func TestStore_RemoveHLSSession(t *testing.T) {
	s := NewStore()
	s.AddHLSSession(&HLSSession{StreamPath: "live/stream"})

	s.RemoveHLSSession("live/stream")

	_, ok := s.GetHLSSession("live/stream")
	assert.False(t, ok)
}

func TestStore_UpdateTelemetry(t *testing.T) {
	s := NewStore()

	telemetry := &Telemetry{
		CPU:     25.5,
		MemMB:   512.0,
		Frame:   1000,
		FPS:     30.0,
		Bitrate: 2500.0,
		Speed:   1.0,
	}

	s.UpdateTelemetry(12345, telemetry)

	got, ok := s.GetTelemetry(12345)
	assert.True(t, ok)
	assert.Equal(t, 25.5, got.CPU)
	assert.Equal(t, 512.0, got.MemMB)
	assert.Equal(t, int64(1000), got.Frame)
	assert.Equal(t, 30.0, got.FPS)
	assert.Equal(t, 2500.0, got.Bitrate)
	assert.Equal(t, 1.0, got.Speed)
}

func TestStore_RemoveTelemetry(t *testing.T) {
	s := NewStore()
	s.UpdateTelemetry(12345, &Telemetry{CPU: 10.0})

	s.RemoveTelemetry(12345)

	_, ok := s.GetTelemetry(12345)
	assert.False(t, ok)
}

func TestStore_TakeSnapshot(t *testing.T) {
	s := NewStore()
	s.AddInput(&Input{StreamPath: "live/stream", RemoteURL: "rtsp://test"})
	s.AddOutput(&Output{StreamPath: "live/stream", OutputID: "out1", RemoteURL: "rtmp://test"})

	snap := s.TakeSnapshot()

	assert.Len(t, snap.Inputs, 1)
	assert.Len(t, snap.Outputs, 1)
	assert.Equal(t, "live/stream", snap.Inputs[0].StreamPath)
	assert.Equal(t, "out1", snap.Outputs[0].OutputID)
}

func TestStore_LoadSnapshot(t *testing.T) {
	s := NewStore()

	snap := &Snapshot{
		Inputs: []*Input{
			{StreamPath: "stream1", Mode: InputModePull},
			{StreamPath: "stream2", Mode: InputModeAccept},
		},
		Outputs: []*Output{
			{StreamPath: "stream1", OutputID: "out1", RemoteURL: "rtmp://test"},
		},
	}

	s.LoadSnapshot(snap)

	inputs := s.ListInputs()
	assert.Len(t, inputs, 2)

	outputs := s.ListOutputs()
	assert.Len(t, outputs, 1)

	for _, in := range inputs {
		assert.Equal(t, InputStatusStopped, in.Status)
	}
}

func TestStore_OnChange(t *testing.T) {
	s := NewStore()
	called := make(chan bool, 1)

	s.OnChange = func() {
		called <- true
	}

	s.AddInput(&Input{StreamPath: "test"})

	select {
	case <-called:
		// success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnChange was not called")
	}
}

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	s := NewStore()
	s.AddInput(&Input{
		StreamPath:  "live/stream",
		RemoteURL:   "rtsp://camera",
		IngestToken: "secret",
		Mode:        InputModePull,
	})
	s.AddOutput(&Output{
		StreamPath: "live/stream",
		OutputID:   "youtube",
		RemoteURL:  "rtmp://youtube.com/live/key",
	})

	snap := s.TakeSnapshot()
	s.LoadSnapshot(snap)

	inputs := s.ListInputs()
	assert.Len(t, inputs, 1)
	assert.Equal(t, "secret", inputs[0].IngestToken)
}

func TestInputMode_Constants(t *testing.T) {
	assert.Equal(t, InputMode("pull"), InputModePull)
	assert.Equal(t, InputMode("accept"), InputModeAccept)
}

func TestInputStatus_Constants(t *testing.T) {
	assert.Equal(t, InputStatus("Starting"), InputStatusStarting)
	assert.Equal(t, InputStatus("Active"), InputStatusActive)
	assert.Equal(t, InputStatus("Stopped"), InputStatusStopped)
	assert.Equal(t, InputStatus("Error"), InputStatusError)
}

func TestOutputStatus_Constants(t *testing.T) {
	assert.Equal(t, OutputStatus("Starting"), OutputStatusStarting)
	assert.Equal(t, OutputStatus("Running"), OutputStatusRunning)
	assert.Equal(t, OutputStatus("Stopped"), OutputStatusStopped)
	assert.Equal(t, OutputStatus("Error"), OutputStatusError)
}

func TestRecordingStatus_Constants(t *testing.T) {
	assert.Equal(t, RecordingStatus("Active"), RecordingStatusActive)
	assert.Equal(t, RecordingStatus("Stopped"), RecordingStatusStopped)
}

func TestOutputKey(t *testing.T) {
	key := outputKey("stream1", "youtube")
	assert.Equal(t, "stream1/youtube", key)
}
