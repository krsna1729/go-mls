package stream

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-mls/internal/logger"

	"github.com/stretchr/testify/assert"
)

type mockPipeline struct {
	*Pipeline
	inputs    map[string]string
	started   []string
	stopped   []string
	listCalls int
}

func newMockPipeline() *Pipeline {
	log := logger.NewLogger()
	p := NewPipeline(log, "/tmp", 60*time.Second)
	p.SetFFmpegFactory(NewTestFFmpegFactory())
	return p
}

func TestApiStartOutputRelay_MissingInputName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartOutputRelay(p)

	body := `{"output_name": "out1", "input_url": "rtsp://test"}`
	req := httptest.NewRequest("POST", "/api/relay/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Contains(t, resp["error"], "required")
}

func TestApiStartOutputRelay_MissingOutputName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartOutputRelay(p)

	body := `{"input_name": "in1", "input_url": "rtsp://test"}`
	req := httptest.NewRequest("POST", "/api/relay/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiStartOutputRelay_InvalidJSON(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartOutputRelay(p)

	body := `{invalid json}`
	req := httptest.NewRequest("POST", "/api/relay/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiStopOutputRelay_MissingOutputName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStopOutputRelay(p)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/relay/stop", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiRelayStatus_ReturnsCorrectFormat(t *testing.T) {
	p := newMockPipeline()
	handler := ApiRelayStatus(p)

	req := httptest.NewRequest("GET", "/api/relay/status", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp StatusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.NotNil(t, resp.Relays)
}

func TestApiStopRecording_MissingName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStopRecording(p)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/recording/stop", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiListRecordings_ReturnsArray(t *testing.T) {
	p := newMockPipeline()
	handler := ApiListRecordings(p)

	req := httptest.NewRequest("GET", "/api/recording/list", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []RecordingListItem
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	if resp != nil {
		assert.IsType(t, []RecordingListItem{}, resp)
	}
}

func TestApiDeleteRecording_MissingFilename(t *testing.T) {
	p := newMockPipeline()
	handler := ApiDeleteRecording(p)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/recording/delete", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiDownloadRecording_MissingFilename(t *testing.T) {
	p := newMockPipeline()
	handler := ApiDownloadRecording(p)

	req := httptest.NewRequest("GET", "/api/recording/download", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiStartRecording_MissingName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartRecording(p)

	body := `{"input_name": "in1"}`
	req := httptest.NewRequest("POST", "/api/recording/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiStartRecording_MissingInputName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartRecording(p)

	body := `{"name": "rec1"}`
	req := httptest.NewRequest("POST", "/api/recording/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiStartHLSViewer_MissingInputName(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartHLSViewer(p)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/relay/hls/start-viewer", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiStartHLSViewer_InputNotFound(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStartHLSViewer(p)

	body := `{"input_name": "nonexistent"}`
	req := httptest.NewRequest("POST", "/api/relay/hls/start-viewer", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestApiStopHLSViewer_MissingFields(t *testing.T) {
	p := newMockPipeline()
	handler := ApiStopHLSViewer(p)

	tests := []struct {
		name string
		body string
	}{
		{"missing both", `{}`},
		{"missing viewer_id", `{"input_name": "in1"}`},
		{"missing input_name", `{"viewer_id": "v1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/relay/hls/stop-viewer", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestApiHLSViewerHeartbeat_MissingFields(t *testing.T) {
	p := newMockPipeline()
	handler := ApiHLSViewerHeartbeat(p)

	tests := []struct {
		name string
		body string
	}{
		{"missing both", `{}`},
		{"missing viewer_id", `{"input_name": "in1"}`},
		{"missing input_name", `{"viewer_id": "v1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/relay/hls/heartbeat", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestApiDeleteInput_InvalidJSON(t *testing.T) {
	p := newMockPipeline()
	handler := ApiDeleteInput(p)

	body := `{invalid}`
	req := httptest.NewRequest("POST", "/api/relay/delete-input", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApiDeleteOutput_InvalidJSON(t *testing.T) {
	p := newMockPipeline()
	handler := ApiDeleteOutput(p)

	body := `{invalid}`
	req := httptest.NewRequest("POST", "/api/relay/delete-output", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPipeline_ListRecordings_Empty(t *testing.T) {
	p := newMockPipeline()
	recordings := p.ListRecordings()
	if recordings != nil {
		assert.Empty(t, recordings)
	}
}

func TestPipeline_Status_ReturnsServerInfo(t *testing.T) {
	p := newMockPipeline()
	status := p.Status()

	assert.NotNil(t, status.Server)
}

func TestPipelineRelay_PresenceCheck(t *testing.T) {
	p := newMockPipeline()
	p.mu.Lock()
	p.relays["test"] = &Relay{}
	p.mu.Unlock()

	p.mu.RLock()
	_, exists := p.relays["test"]
	p.mu.RUnlock()

	assert.True(t, exists)

	p.mu.RLock()
	_, notExists := p.relays["nonexistent"]
	p.mu.RUnlock()

	assert.False(t, notExists)
}

func TestRecordingFilenameScheme(t *testing.T) {
	rec := &PipelineRecording{
		Name:     "myrecording",
		Filename: "myrecording_1234567890.mp4",
	}

	assert.Contains(t, rec.Filename, "myrecording")
	assert.Contains(t, rec.Filename, "_")
	assert.Contains(t, rec.Filename, ".mp4")
}

func TestHLSSessionViewerLifecycle(t *testing.T) {
	sess := NewPipelineHLSSession("test-session", "rtsp://test")

	assert.Equal(t, "test-session", sess.Name)
	assert.Equal(t, 0, len(sess.ViewerIDs))

	sess.Mu.Lock()
	sess.ViewerIDs["viewer-1"] = time.Now()
	sess.Mu.Unlock()

	sess.Mu.RLock()
	hasViewer := func() bool {
		_, ok := sess.ViewerIDs["viewer-1"]
		return ok
	}()
	sess.Mu.RUnlock()

	assert.True(t, hasViewer)
}

func TestPresetApplication(t *testing.T) {
	opts := ApplyPresetAndOptions("YouTube", nil)
	assert.Equal(t, "libx264", opts.VideoCodec)
	assert.Equal(t, "aac", opts.AudioCodec)

	optsWithOverride := ApplyPresetAndOptions("YouTube", map[string]string{
		"resolution": "1280x720",
		"bitrate":    "2500k",
	})
	assert.Equal(t, "libx264", optsWithOverride.VideoCodec)
	assert.Equal(t, "1280x720", optsWithOverride.Resolution)
	assert.Equal(t, "2500k", optsWithOverride.Bitrate)
}

func TestFFmpegOptsConversion(t *testing.T) {
	opts := FFmpegOpts{
		VideoCodec: "libx264",
		AudioCodec: "aac",
		Resolution: "1920x1080",
		Framerate:  "30",
		Bitrate:    "4500k",
	}

	m := FFmpegOptsToMap(opts)
	assert.Equal(t, "libx264", m["video_codec"])
	assert.Equal(t, "aac", m["audio_codec"])
	assert.Equal(t, "1920x1080", m["resolution"])

	restored := FFmpegOptsFromMap(m)
	assert.Equal(t, opts.VideoCodec, restored.VideoCodec)
	assert.Equal(t, opts.AudioCodec, restored.AudioCodec)
	assert.Equal(t, opts.Resolution, restored.Resolution)
}
