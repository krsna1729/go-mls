// Package api implements the HTTP Control Plane for go-mls.
// It exposes all REST endpoints defined in the REFACTOR.md spec.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-mls/internal/ingest"
	"go-mls/internal/logger"
	"go-mls/internal/state"
	"go-mls/internal/worker"

	"github.com/shirou/gopsutil/v3/process"
)

// Server is the HTTP API server.
type Server struct {
	store      *state.Store
	ingest     *ingest.Router
	hlsMgr     *worker.HLSManager
	log        *logger.Logger
	recDir     string
	hlsDir     string
	rtmpPort   int
	configPath string
	ctx        context.Context
	recBroker  *recordingsBroker
	recWatch   *recordingsWatcher

	// Active workers tracking
	mu          sync.RWMutex
	restreamers map[string]*worker.Restreamer // keyed by stream_path/output_id
	recorders   map[string]*worker.Recorder   // keyed by stream_path

	importMu         sync.Mutex
	importInProgress bool

	statsCache     atomic.Value
	statsCacheStop chan struct{}
	statsCacheWG   sync.WaitGroup
}

type statsCacheSnapshot struct {
	body []byte
}

type stoppableWorker interface {
	Stop()
	Wait() error
}

const (
	statsCacheRefreshInterval = 500 * time.Millisecond
	importInputReadyTimeout   = 15 * time.Second
	importWorkerStopTimeout   = 5 * time.Second
)

// NewServer creates a new HTTP API server.
func NewServer(
	store *state.Store,
	ingestRouter *ingest.Router,
	hlsMgr *worker.HLSManager,
	log *logger.Logger,
	recDir string,
	hlsDir string,
	rtmpPort int,
	configPath string,
	ctx context.Context,
) *Server {
	recBroker := newRecordingsBroker()
	recWatch, err := newRecordingsWatcher(recDir, recBroker, func(msg string, args ...interface{}) {
		log.Debug(fmt.Sprintf(msg, args...))
	})
	if err != nil {
		log.Warn("Failed to start recordings watcher", "dir", recDir, "error", err)
	}

	s := &Server{
		store:          store,
		ingest:         ingestRouter,
		hlsMgr:         hlsMgr,
		log:            log.With("component", "api"),
		recDir:         recDir,
		hlsDir:         hlsDir,
		rtmpPort:       rtmpPort,
		configPath:     configPath,
		ctx:            ctx,
		recBroker:      recBroker,
		recWatch:       recWatch,
		restreamers:    make(map[string]*worker.Restreamer),
		recorders:      make(map[string]*worker.Recorder),
		statsCacheStop: make(chan struct{}),
	}

	s.refreshStatsCache()
	s.startStatsCacheLoop()

	return s
}

// RegisterRoutes registers all API endpoints on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Core REFACTOR.md endpoints
	mux.HandleFunc("/inputs", s.handleInputs)
	mux.HandleFunc("/outputs", s.handleOutputs)
	mux.HandleFunc("/outputs/start", s.handleOutputStart)
	mux.HandleFunc("/outputs/stop", s.handleOutputStop)
	mux.HandleFunc("/presets", s.handlePresets)
	mux.HandleFunc("/record", s.handleRecord)
	mux.HandleFunc("/recordings", s.handleRecordings)
	mux.HandleFunc("/recordings/sse", s.handleRecordingsSSE)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/system/export", s.handleExport)
	mux.HandleFunc("/system/import", s.handleImport)

	// HLS endpoints
	mux.HandleFunc("/hls/start", s.handleHLSStart)
	mux.HandleFunc("/hls/stop", s.handleHLSStop)
	mux.HandleFunc("/hls/heartbeat", s.handleHLSHeartbeat)

	// HLS file serving - serve from hls directory
	if s.hlsDir != "" {
		hlsFS := http.FileServer(http.Dir(s.hlsDir))
		mux.Handle("/hls/", http.StripPrefix("/hls/", hlsFS))
	}

	// Recordings file serving - serve from recordings directory
	if s.recDir != "" {
		mux.HandleFunc("/recordings/download", s.handleRecordingDownload)
		recFS := http.FileServer(http.Dir(s.recDir))
		mux.Handle("/recordings/", http.StripPrefix("/recordings/", recFS))
	}
}

// --- /inputs ---

type inputRequest struct {
	StreamPath  string `json:"stream_path"`
	RemoteURL   string `json:"remote_url,omitempty"`
	IngestToken string `json:"ingest_token,omitempty"`
}

func (s *Server) handleInputs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createInput(w, r)
	case http.MethodDelete:
		s.deleteInput(w, r)
	case http.MethodGet:
		s.listInputs(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) createInput(w http.ResponseWriter, r *http.Request) {
	var req inputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.StreamPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream_path required"})
		return
	}

	in := &state.Input{
		StreamPath:  req.StreamPath,
		RemoteURL:   req.RemoteURL,
		IngestToken: req.IngestToken,
	}

	if err := s.ingest.RegisterInput(s.ctx, in); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	s.log.Info("Input registered", "stream_path", req.StreamPath)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "stream_path": req.StreamPath})
}

func (s *Server) deleteInput(w http.ResponseWriter, r *http.Request) {
	streamPath := r.URL.Query().Get("stream")
	if streamPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream query param required"})
		return
	}

	s.mu.Lock()
	// Stop all outputs for this input first
	for key, rs := range s.restreamers {
		if strings.HasPrefix(key, streamPath+"/") {
			if rs != nil {
				rs.Stop()
			}
			delete(s.restreamers, key)
		}
	}

	// Remove all output definitions for this input, including already-stopped outputs.
	for _, out := range s.store.ListOutputs() {
		if out.StreamPath == streamPath {
			s.store.RemoveOutput(streamPath, out.OutputID)
		}
	}

	// Stop recording if active
	if rec, ok := s.recorders[streamPath]; ok {
		rec.Stop()
		delete(s.recorders, streamPath)
		s.store.RemoveRecording(streamPath)
	}
	s.mu.Unlock()

	if s.hlsMgr != nil {
		s.hlsMgr.StopStream(streamPath)
	}

	if err := s.ingest.UnregisterInput(streamPath); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	s.log.Info("Input removed", "stream_path", streamPath)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listInputs(w http.ResponseWriter, _ *http.Request) {
	inputs := s.store.ListInputs()
	writeJSON(w, http.StatusOK, inputs)
}

// --- /outputs ---

type outputRequest struct {
	StreamPath string   `json:"stream_path"`
	OutputID   string   `json:"output_id"`
	RemoteURL  string   `json:"remote_url"`
	StreamKey  string   `json:"stream_key,omitempty"`
	Preset     string   `json:"preset,omitempty"`
	VideoCodec string   `json:"video_codec,omitempty"`
	AudioCodec string   `json:"audio_codec,omitempty"`
	Resolution string   `json:"resolution,omitempty"`
	Framerate  string   `json:"framerate,omitempty"`
	Bitrate    string   `json:"bitrate,omitempty"`
	Rotation   string   `json:"rotation,omitempty"`
	VideoArgs  []string `json:"video_args,omitempty"`
	AudioArgs  []string `json:"audio_args,omitempty"`
}

type outputActionRequest struct {
	StreamPath string `json:"stream_path"`
	OutputID   string `json:"output_id"`
}

func (s *Server) waitForInputActive(streamPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	lastEnsure := time.Time{}
	lastErr := ""
	for time.Now().Before(deadline) {
		in, ok := s.store.GetInput(streamPath)
		if !ok {
			return fmt.Errorf("input %q not found", streamPath)
		}
		switch in.Status {
		case state.InputStatusActive:
			return nil
		case state.InputStatusError:
			if in.LastError != "" {
				lastErr = in.LastError
			} else {
				lastErr = "input entered error state"
			}
		}

		// Keep nudging input activation while waiting.
		// For pull inputs this restarts failed pullers; for accept inputs it's a no-op.
		if time.Since(lastEnsure) >= time.Second {
			if err := s.ingest.EnsureInputActive(s.ctx, streamPath); err != nil {
				if lastErr == "" {
					lastErr = err.Error()
				}
			} else {
				lastEnsure = time.Now()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	if lastErr != "" {
		return fmt.Errorf("input %q did not become active in time: %s", streamPath, lastErr)
	}
	return fmt.Errorf("input %q did not become active in time", streamPath)
}

func (s *Server) stopWorkersAndWait(kind string, timeout time.Duration, workers ...stoppableWorker) {
	activeWorkers := make([]stoppableWorker, 0, len(workers))
	for _, worker := range workers {
		if worker != nil {
			activeWorkers = append(activeWorkers, worker)
		}
	}
	if len(activeWorkers) == 0 {
		return
	}

	var wg sync.WaitGroup
	done := make(chan struct{})
	for _, worker := range activeWorkers {
		wg.Add(1)
		go func(worker stoppableWorker) {
			defer wg.Done()
			worker.Stop()
			if err := worker.Wait(); err != nil {
				s.log.Debug("Worker exited while draining import runtime", "kind", kind, "error", err)
			}
		}(worker)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		s.log.Warn("Timed out waiting for runtime workers to stop during import", "kind", kind, "count", len(activeWorkers), "timeout", timeout)
	}
}

func (s *Server) handleOutputs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createOutput(w, r)
	case http.MethodDelete:
		s.deleteOutput(w, r)
	case http.MethodGet:
		s.listOutputs(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) createOutput(w http.ResponseWriter, r *http.Request) {
	var req outputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.StreamPath == "" || req.OutputID == "" || req.RemoteURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream_path, output_id, and remote_url required"})
		return
	}

	// Verify input exists
	if _, ok := s.store.GetInput(req.StreamPath); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("input %q not found", req.StreamPath)})
		return
	}

	videoArgs := req.VideoArgs
	audioArgs := req.AudioArgs

	if req.Preset != "" || req.VideoCodec != "" || req.AudioCodec != "" || req.Resolution != "" || req.Framerate != "" || req.Bitrate != "" || req.Rotation != "" {
		preset, hasPreset := state.GetPreset(req.Preset)
		if !hasPreset && req.Preset != "" && req.Preset != "Custom" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown preset %q", req.Preset)})
			return
		}

		presetVideoArgs, presetAudioArgs := preset.ToArgs()

		if req.VideoCodec != "" {
			presetVideoArgs = append(presetVideoArgs, "-c:v", req.VideoCodec)
		}
		if req.AudioCodec != "" {
			presetAudioArgs = append(presetAudioArgs, "-c:a", req.AudioCodec)
		}
		if req.Resolution != "" {
			presetVideoArgs = append(presetVideoArgs, "-s", req.Resolution)
		}
		if req.Framerate != "" {
			presetVideoArgs = append(presetVideoArgs, "-r", req.Framerate)
		}
		if req.Bitrate != "" {
			presetVideoArgs = append(presetVideoArgs, "-b:v", req.Bitrate)
		}
		if req.Rotation != "" {
			presetVideoArgs = append(presetVideoArgs, "-vf", req.Rotation)
		}

		if len(videoArgs) == 0 {
			videoArgs = presetVideoArgs
		}
		if len(audioArgs) == 0 {
			audioArgs = presetAudioArgs
		}
	}

	out := &state.Output{
		StreamPath:     req.StreamPath,
		OutputID:       req.OutputID,
		RemoteURL:      req.RemoteURL,
		StreamKey:      req.StreamKey,
		VideoArgs:      videoArgs,
		AudioArgs:      audioArgs,
		PlatformPreset: req.Preset,
		FFmpegOptions:  buildFFmpegOptions(req),
		Status:         state.OutputStatusStarting,
	}

	if err := s.store.AddOutput(out); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	if err := s.ingest.EnsureInputActive(s.ctx, req.StreamPath); err != nil {
		s.store.RemoveOutput(req.StreamPath, req.OutputID)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := s.waitForInputActive(req.StreamPath, 15*time.Second); err != nil {
		s.store.RemoveOutput(req.StreamPath, req.OutputID)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	// Start the restreamer
	rs, err := worker.StartRestreamer(s.ctx, s.store, s.log, out, s.rtmpPort)
	if err != nil {
		s.store.RemoveOutput(req.StreamPath, req.OutputID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	key := req.StreamPath + "/" + req.OutputID
	s.mu.Lock()
	s.restreamers[key] = rs
	s.mu.Unlock()

	s.log.Info("Output started", "stream_path", req.StreamPath, "output_id", req.OutputID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "output_id": req.OutputID})
}

func (s *Server) handleOutputStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req outputActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.StreamPath == "" || req.OutputID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream_path and output_id required"})
		return
	}

	out, ok := s.store.GetOutput(req.StreamPath, req.OutputID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("output %q/%q not found", req.StreamPath, req.OutputID)})
		return
	}
	if _, ok := s.store.GetInput(req.StreamPath); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("input %q not found", req.StreamPath)})
		return
	}

	if err := s.ingest.EnsureInputActive(s.ctx, req.StreamPath); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := s.waitForInputActive(req.StreamPath, 15*time.Second); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if out.Status == state.OutputStatusRunning {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "output_id": req.OutputID})
		return
	}

	key := req.StreamPath + "/" + req.OutputID
	s.mu.Lock()
	if _, running := s.restreamers[key]; running {
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "output_id": req.OutputID})
		return
	}
	s.mu.Unlock()

	s.store.UpdateOutputStatus(req.StreamPath, req.OutputID, state.OutputStatusStarting, "")
	rs, err := worker.StartRestreamer(s.ctx, s.store, s.log, out, s.rtmpPort)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.restreamers[key] = rs
	s.mu.Unlock()

	s.log.Info("Output restarted", "stream_path", req.StreamPath, "output_id", req.OutputID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "output_id": req.OutputID})
}

func (s *Server) handleOutputStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req outputActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.StreamPath == "" || req.OutputID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream_path and output_id required"})
		return
	}

	if _, ok := s.store.GetOutput(req.StreamPath, req.OutputID); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("output %q/%q not found", req.StreamPath, req.OutputID)})
		return
	}

	key := req.StreamPath + "/" + req.OutputID
	s.mu.Lock()
	rs, ok := s.restreamers[key]
	if ok {
		delete(s.restreamers, key)
	}
	s.mu.Unlock()

	if ok {
		if rs != nil {
			rs.Stop()
		}
	} else {
		s.store.UpdateOutputStatus(req.StreamPath, req.OutputID, state.OutputStatusStopped, "")
	}

	s.log.Info("Output stopped", "stream_path", req.StreamPath, "output_id", req.OutputID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "output_id": req.OutputID})
}

func (s *Server) deleteOutput(w http.ResponseWriter, r *http.Request) {
	streamPath := r.URL.Query().Get("stream")
	outputID := r.URL.Query().Get("id")
	if streamPath == "" || outputID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream and id query params required"})
		return
	}

	key := streamPath + "/" + outputID
	s.mu.Lock()
	if rs, ok := s.restreamers[key]; ok {
		if rs != nil {
			rs.Stop()
		}
		delete(s.restreamers, key)
	}
	s.mu.Unlock()

	if err := s.store.RemoveOutput(streamPath, outputID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	s.log.Info("Output stopped", "stream_path", streamPath, "output_id", outputID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listOutputs(w http.ResponseWriter, r *http.Request) {
	streamPath := r.URL.Query().Get("stream")
	if streamPath != "" {
		outputs := s.store.ListOutputsForInput(streamPath)
		writeJSON(w, http.StatusOK, outputs)
	} else {
		outputs := s.store.ListOutputs()
		writeJSON(w, http.StatusOK, outputs)
	}
}

// --- /record ---

func (s *Server) handleRecord(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.startRecord(w, r)
	case http.MethodDelete:
		s.stopRecord(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) startRecord(w http.ResponseWriter, r *http.Request) {
	streamPath := r.URL.Query().Get("stream")
	if streamPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream query param required"})
		return
	}

	// Verify input exists
	if _, ok := s.store.GetInput(streamPath); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("input %q not found", streamPath)})
		return
	}

	rec, err := worker.StartRecorder(s.ctx, s.store, s.log, streamPath, s.recDir, s.rtmpPort)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.recorders[streamPath] = rec
	s.mu.Unlock()

	s.log.Info("Recording started", "stream_path", streamPath)
	s.notifyRecordingsChanged()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "stream_path": streamPath})
}

func (s *Server) stopRecord(w http.ResponseWriter, r *http.Request) {
	streamPath := r.URL.Query().Get("stream")
	if streamPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream query param required"})
		return
	}

	s.mu.Lock()
	rec, ok := s.recorders[streamPath]
	if ok {
		rec.Stop()
		delete(s.recorders, streamPath)
	}
	s.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active recording"})
		return
	}

	s.store.RemoveRecording(streamPath)

	s.log.Info("Recording stopped", "stream_path", streamPath)
	s.notifyRecordingsChanged()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- /recordings ---

type recordingEntry struct {
	StreamPath string    `json:"stream_path"`
	Name       string    `json:"name"`
	Filename   string    `json:"filename"`
	StartedAt  time.Time `json:"started_at"`
	FileSize   int64     `json:"file_size"`
	Active     bool      `json:"active"`
}

var recordingFilenamePattern = regexp.MustCompile(`^(?s)(.+)_(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2})\.mp4$`)

func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listRecordings(w, r)
	case http.MethodDelete:
		s.deleteRecording(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listRecordings(w http.ResponseWriter, _ *http.Request) {
	entries, err := s.collectRecordings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) deleteRecording(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename query param required"})
		return
	}

	for _, rec := range s.store.ListRecordings() {
		if rec.Filename == filename {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "recording is still active"})
			return
		}
	}

	fullPath, err := resolveRecordingPath(s.recDir, filename)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "recording not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	pruneEmptyRecordingDirs(s.recDir, filepath.Dir(fullPath))
	s.notifyRecordingsChanged()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRecordingDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filename := r.URL.Query().Get("filename")
	if filename == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename query param required"})
		return
	}

	fullPath, err := resolveRecordingPath(s.recDir, filename)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	f, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "recording not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(filename)))
	http.ServeContent(w, r, filepath.Base(filename), st.ModTime(), f)
}

func (s *Server) handleRecordingsSSE(w http.ResponseWriter, r *http.Request) {
	if s.recBroker == nil {
		http.Error(w, "recordings SSE unavailable", http.StatusServiceUnavailable)
		return
	}
	s.recBroker.Handler().ServeHTTP(w, r)
}

// --- /stats ---

type statsResponse struct {
	Server  serverStats   `json:"server"`
	Inputs  []inputStats  `json:"inputs"`
	Outputs []outputStats `json:"outputs"`
}

type serverStats struct {
	CPU float64 `json:"cpu"`
	Mem float64 `json:"mem_mb"`
}

type inputStats struct {
	StreamPath string            `json:"stream_path"`
	Mode       state.InputMode   `json:"mode"`
	Status     state.InputStatus `json:"status"`
	RemoteURL  string            `json:"remote_url,omitempty"`
	RemoteAddr string            `json:"remote_addr,omitempty"`
	LastError  string            `json:"last_error,omitempty"`
	Telemetry  *state.Telemetry  `json:"telemetry,omitempty"`
}

type outputStats struct {
	StreamPath string             `json:"stream_path"`
	OutputID   string             `json:"output_id"`
	RemoteURL  string             `json:"remote_url"`
	Status     state.OutputStatus `json:"status"`
	LastError  string             `json:"last_error,omitempty"`
	Telemetry  *state.Telemetry   `json:"telemetry,omitempty"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if cached, ok := s.statsCache.Load().(statsCacheSnapshot); ok && len(cached.body) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cached.body)
		return
	}

	http.Error(w, "stats unavailable", http.StatusServiceUnavailable)
}

func (s *Server) startStatsCacheLoop() {
	s.statsCacheWG.Add(1)
	go func() {
		defer s.statsCacheWG.Done()

		var selfProc *process.Process
		ticker := time.NewTicker(statsCacheRefreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.refreshStatsCacheWithProcess(&selfProc)
			case <-s.statsCacheStop:
				return
			}
		}
	}()
}

func (s *Server) refreshStatsCache() {
	var selfProc *process.Process
	s.refreshStatsCacheWithProcess(&selfProc)
}

func (s *Server) refreshStatsCacheWithProcess(selfProc **process.Process) {
	resp := s.buildStatsResponse(selfProc)
	body, err := json.Marshal(resp)
	if err != nil {
		s.log.Warn("Failed to refresh stats cache", "error", err)
		return
	}
	s.statsCache.Store(statsCacheSnapshot{body: body})
}

func (s *Server) buildStatsResponse(selfProc **process.Process) statsResponse {
	inputs := s.store.ListInputs()
	outputs := s.store.ListOutputs()

	resp := statsResponse{
		Server:  s.getSelfStats(selfProc),
		Inputs:  make([]inputStats, 0, len(inputs)),
		Outputs: make([]outputStats, 0, len(outputs)),
	}

	for _, in := range inputs {
		is := inputStats{
			StreamPath: in.StreamPath,
			Mode:       in.Mode,
			Status:     in.Status,
			RemoteURL:  in.RemoteURL,
			RemoteAddr: in.RemoteAddr,
			LastError:  in.LastError,
		}
		if in.PID > 0 {
			if t, ok := s.store.GetTelemetry(in.PID); ok {
				is.Telemetry = cloneTelemetry(t)
			}
		}
		resp.Inputs = append(resp.Inputs, is)
	}

	for _, out := range outputs {
		os := outputStats{
			StreamPath: out.StreamPath,
			OutputID:   out.OutputID,
			RemoteURL:  out.RemoteURL,
			Status:     out.Status,
			LastError:  out.LastError,
		}
		if out.PID > 0 {
			if t, ok := s.store.GetTelemetry(out.PID); ok {
				os.Telemetry = cloneTelemetry(t)
			}
		}
		resp.Outputs = append(resp.Outputs, os)
	}

	return resp
}

func (s *Server) getSelfStats(selfProc **process.Process) serverStats {
	stats := serverStats{}
	if selfProc == nil {
		return stats
	}
	if *selfProc == nil {
		proc, err := process.NewProcess(int32(os.Getpid()))
		if err != nil {
			return stats
		}
		*selfProc = proc
	}

	if cpu, err := (*selfProc).CPUPercent(); err == nil {
		stats.CPU = cpu
	} else {
		*selfProc = nil
	}
	if *selfProc != nil {
		if mem, err := (*selfProc).MemoryInfo(); err == nil {
			stats.Mem = float64(mem.RSS) / (1024 * 1024)
		} else {
			*selfProc = nil
		}
	}

	return stats
}

func cloneTelemetry(t *state.Telemetry) *state.Telemetry {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}

// --- /system/export ---

type exportOutput struct {
	OutputURL      string            `json:"output_url"`
	OutputName     string            `json:"output_name"`
	PlatformPreset string            `json:"platform_preset,omitempty"`
	FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
}

type exportRelay struct {
	InputURL  string         `json:"input_url"`
	InputName string         `json:"input_name"`
	Outputs   []exportOutput `json:"outputs"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	inputs := s.store.ListInputs()
	outputs := s.store.ListOutputs()

	relays := make(map[string]*exportRelay)
	for _, in := range inputs {
		relays[in.StreamPath] = &exportRelay{
			InputURL:  in.RemoteURL,
			InputName: in.StreamPath,
			Outputs:   []exportOutput{},
		}
	}

	for _, out := range outputs {
		relay, ok := relays[out.StreamPath]
		if !ok {
			continue
		}
		relay.Outputs = append(relay.Outputs, exportOutput{
			OutputURL:      out.RemoteURL,
			OutputName:     out.OutputID,
			PlatformPreset: out.PlatformPreset,
			FFmpegOptions:  exportFFmpegOptions(&out),
		})
	}

	result := make([]exportRelay, 0, len(relays))
	for _, relay := range relays {
		result = append(result, *relay)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"relay_config.json\"")
	w.Write(data)
}

// --- /system/import ---

type importOutput struct {
	OutputURL      string            `json:"output_url"`
	OutputName     string            `json:"output_name"`
	PlatformPreset string            `json:"platform_preset,omitempty"`
	FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
}

type importRelay struct {
	InputURL  string         `json:"input_url"`
	InputName string         `json:"input_name"`
	Outputs   []importOutput `json:"outputs"`
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var relays []importRelay
	if err := json.NewDecoder(r.Body).Decode(&relays); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	s.importMu.Lock()
	if s.importInProgress {
		s.importMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "import already in progress"})
		return
	}
	s.importInProgress = true
	s.importMu.Unlock()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})

	go func(relays []importRelay) {
		defer func() {
			s.importMu.Lock()
			s.importInProgress = false
			s.importMu.Unlock()
		}()
		s.applyImport(relays)
	}(relays)
}

func (s *Server) applyImport(relays []importRelay) {
	// Stop all current workers first so import writes a clean runtime state.
	s.mu.Lock()
	restreamers := s.restreamers
	recorders := s.recorders
	s.restreamers = make(map[string]*worker.Restreamer)
	s.recorders = make(map[string]*worker.Recorder)
	s.mu.Unlock()

	restreamerWorkers := make([]stoppableWorker, 0, len(restreamers))
	for _, rs := range restreamers {
		restreamerWorkers = append(restreamerWorkers, rs)
	}
	recorderWorkers := make([]stoppableWorker, 0, len(recorders))
	for _, rec := range recorders {
		recorderWorkers = append(recorderWorkers, rec)
	}
	s.stopWorkersAndWait("restreamer", importWorkerStopTimeout, restreamerWorkers...)
	s.stopWorkersAndWait("recorder", importWorkerStopTimeout, recorderWorkers...)

	// Clear existing routing/state definitions before recreating from imported payload.
	for _, in := range s.store.ListInputs() {
		if err := s.ingest.UnregisterInput(in.StreamPath); err != nil {
			s.log.Warn("Failed to unregister input during import", "stream_path", in.StreamPath, "error", err)
		}
	}
	for _, out := range s.store.ListOutputs() {
		if err := s.store.RemoveOutput(out.StreamPath, out.OutputID); err != nil {
			s.log.Warn("Failed to remove output during import", "stream_path", out.StreamPath, "output_id", out.OutputID, "error", err)
		}
	}

	var totalInputs atomic.Int64
	var totalOutputs atomic.Int64
	var startedOutputs atomic.Int64
	var wg sync.WaitGroup

	for _, relay := range relays {
		relay := relay
		wg.Add(1)
		go func() {
			defer wg.Done()

			inputName := strings.TrimSpace(relay.InputName)
			if inputName == "" {
				s.log.Warn("Skipping imported relay with empty input_name")
				return
			}

			in := &state.Input{StreamPath: inputName, RemoteURL: relay.InputURL}
			if err := s.ingest.RegisterInput(s.ctx, in); err != nil {
				s.log.Error("Failed to register input", "input_name", inputName, "error", err)
				return
			}
			totalInputs.Add(1)

			importedOutputs := make([]*state.Output, 0, len(relay.Outputs))
			for _, out := range relay.Outputs {
				outputID := out.OutputName
				if outputID == "" {
					outputID = out.OutputURL
				}
				if outputID == "" {
					s.log.Warn("Skipping imported output with empty output identifier", "stream_path", inputName)
					continue
				}

				preset, _ := state.GetPreset(out.PlatformPreset)
				videoArgs, audioArgs := preset.ToArgs()

				if opts := out.FFmpegOptions; opts != nil {
					if vc, ok := opts["video_codec"]; ok && vc != "" {
						videoArgs = append(videoArgs, "-c:v", vc)
					}
					if ac, ok := opts["audio_codec"]; ok && ac != "" {
						audioArgs = append(audioArgs, "-c:a", ac)
					}
					if res, ok := opts["resolution"]; ok && res != "" {
						videoArgs = append(videoArgs, "-s", res)
					}
					if fps, ok := opts["framerate"]; ok && fps != "" {
						videoArgs = append(videoArgs, "-r", fps)
					}
					if br, ok := opts["bitrate"]; ok && br != "" {
						videoArgs = append(videoArgs, "-b:v", br)
					}
					if rot, ok := opts["rotation"]; ok && rot != "" {
						videoArgs = append(videoArgs, "-vf", rot)
					}
				}

				stateOut := &state.Output{
					StreamPath:     inputName,
					OutputID:       outputID,
					RemoteURL:      out.OutputURL,
					VideoArgs:      videoArgs,
					AudioArgs:      audioArgs,
					PlatformPreset: out.PlatformPreset,
					FFmpegOptions:  copyStringMap(out.FFmpegOptions),
					Status:         state.OutputStatusStopped,
				}

				if err := s.store.AddOutput(stateOut); err != nil {
					s.log.Error("Failed to add output", "stream_path", inputName, "output_id", outputID, "error", err)
					continue
				}
				importedOutputs = append(importedOutputs, stateOut)
				totalOutputs.Add(1)
			}

			inputReadyErr := s.waitForInputActive(inputName, importInputReadyTimeout)
			if inputReadyErr != nil {
				s.log.Warn("Imported input did not become active before output start", "stream_path", inputName, "error", inputReadyErr)
				for _, stateOut := range importedOutputs {
					s.store.UpdateOutputStatus(stateOut.StreamPath, stateOut.OutputID, state.OutputStatusError, inputReadyErr.Error())
				}
				return
			}

			for _, stateOut := range importedOutputs {
				key := stateOut.StreamPath + "/" + stateOut.OutputID
				s.store.UpdateOutputStatus(stateOut.StreamPath, stateOut.OutputID, state.OutputStatusStarting, "")
				s.mu.Lock()
				s.restreamers[key] = nil
				s.mu.Unlock()
				rs, err := worker.StartRestreamer(s.ctx, s.store, s.log, stateOut, s.rtmpPort)
				if err != nil {
					s.mu.Lock()
					delete(s.restreamers, key)
					s.mu.Unlock()
					s.store.UpdateOutputStatus(stateOut.StreamPath, stateOut.OutputID, state.OutputStatusError, err.Error())
					s.log.Warn("Imported output failed to start", "stream_path", stateOut.StreamPath, "output_id", stateOut.OutputID, "error", err)
					continue
				}

				s.mu.Lock()
				s.restreamers[key] = rs
				s.mu.Unlock()
				startedOutputs.Add(1)
			}
		}()
	}

	wg.Wait()
	s.log.Info("Configuration imported", "inputs", totalInputs.Load(), "outputs", totalOutputs.Load(), "started_outputs", startedOutputs.Load())
}

// --- /hls/start ---

func (s *Server) handleHLSStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	streamPath := r.URL.Query().Get("stream")
	if streamPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream query param required"})
		return
	}

	viewerID, err := s.hlsMgr.AddViewer(s.ctx, streamPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	playlistURL := path.Join("/hls", streamPath, "index.m3u8")
	playlistPath := filepath.Join(s.hlsDir, streamPath, "index.m3u8")

	// Probe for playlist readiness briefly so API calls remain responsive under load.
	deadline := time.Now().Add(3 * time.Second)
	playlistReady := false
	for time.Now().Before(deadline) {
		b, readErr := os.ReadFile(playlistPath)
		if readErr == nil && strings.Contains(string(b), "#EXTM3U") {
			playlistReady = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !playlistReady {
		s.log.Warn("HLS playlist not ready before response timeout", "stream_path", streamPath, "viewer_id", viewerID)
	} else {
		s.log.Info("HLS viewer started", "stream_path", streamPath)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"stream_path":    streamPath,
		"viewer_id":      viewerID,
		"playlist_url":   playlistURL,
		"playlist_ready": playlistReady,
	})
}

// --- /hls/stop ---

func (s *Server) handleHLSStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Stream   string `json:"stream"`
		ViewerID string `json:"viewer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		streamPath := r.URL.Query().Get("stream")
		if streamPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream or body required"})
			return
		}
		req.Stream = streamPath
	}

	if req.Stream == "" || req.ViewerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream and viewer_id required"})
		return
	}

	s.hlsMgr.RemoveViewer(req.Stream, req.ViewerID)
	s.log.Info("HLS viewer stopped", "stream_path", req.Stream, "viewer_id", req.ViewerID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleHLSHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Stream   string `json:"stream"`
		ViewerID string `json:"viewer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Stream == "" || req.ViewerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream and viewer_id required"})
		return
	}

	if err := s.hlsMgr.Heartbeat(req.Stream, req.ViewerID); err != nil {
		if err.Error() == "session not found" {
			writeJSON(w, http.StatusGone, map[string]string{"error": "viewer session expired or stream ended"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- /presets ---

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	presets := state.ListPresets()
	writeJSON(w, http.StatusOK, presets)
}

func buildFFmpegOptions(req outputRequest) map[string]string {
	opts := map[string]string{}
	if req.VideoCodec != "" {
		opts["video_codec"] = req.VideoCodec
	}
	if req.AudioCodec != "" {
		opts["audio_codec"] = req.AudioCodec
	}
	if req.Resolution != "" {
		opts["resolution"] = req.Resolution
	}
	if req.Framerate != "" {
		opts["framerate"] = req.Framerate
	}
	if req.Bitrate != "" {
		opts["bitrate"] = req.Bitrate
	}
	if req.Rotation != "" {
		opts["rotation"] = req.Rotation
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

func exportFFmpegOptions(out *state.Output) map[string]string {
	if len(out.FFmpegOptions) > 0 {
		return copyStringMap(out.FFmpegOptions)
	}

	opts := map[string]string{}
	for i := 0; i < len(out.VideoArgs)-1; i++ {
		switch out.VideoArgs[i] {
		case "-c:v":
			opts["video_codec"] = out.VideoArgs[i+1]
		case "-s":
			opts["resolution"] = out.VideoArgs[i+1]
		case "-r":
			opts["framerate"] = out.VideoArgs[i+1]
		case "-b:v":
			opts["bitrate"] = out.VideoArgs[i+1]
		case "-vf":
			opts["rotation"] = out.VideoArgs[i+1]
		}
	}
	for i := 0; i < len(out.AudioArgs)-1; i++ {
		if out.AudioArgs[i] == "-c:a" {
			opts["audio_codec"] = out.AudioArgs[i+1]
		}
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (s *Server) collectRecordings() ([]recordingEntry, error) {
	entries := map[string]recordingEntry{}
	activeRecordings := s.store.ListRecordings()
	activeByFilename := make(map[string]state.Recording, len(activeRecordings))
	for _, rec := range activeRecordings {
		activeByFilename[filepath.ToSlash(rec.Filename)] = rec
	}

	if s.recDir != "" {
		err := filepath.Walk(s.recDir, func(fullPath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			relPath, err := filepath.Rel(s.recDir, fullPath)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash(relPath)

			streamPath, startedAt := parseRecordingMetadata(relPath, info.ModTime())
			_, active := activeByFilename[relPath]
			entries[relPath] = recordingEntry{
				StreamPath: streamPath,
				Name:       streamPath,
				Filename:   relPath,
				StartedAt:  startedAt,
				FileSize:   info.Size(),
				Active:     active,
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("walk recordings: %w", err)
		}
	}

	for filename, rec := range activeByFilename {
		if _, exists := entries[filename]; exists {
			continue
		}
		entries[filename] = recordingEntry{
			StreamPath: rec.StreamPath,
			Name:       rec.StreamPath,
			Filename:   filepath.ToSlash(rec.Filename),
			StartedAt:  rec.StartedAt,
			Active:     true,
		}
	}

	result := make([]recordingEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.After(result[j].StartedAt)
	})
	return result, nil
}

func parseRecordingMetadata(filename string, fallback time.Time) (string, time.Time) {
	matches := recordingFilenamePattern.FindStringSubmatch(filepath.ToSlash(filename))
	if len(matches) != 3 {
		return strings.TrimSuffix(filepath.ToSlash(filename), filepath.Ext(filename)), fallback
	}

	startedAt, err := time.Parse("2006-01-02_15-04-05", matches[2])
	if err != nil {
		return matches[1], fallback
	}
	return matches[1], startedAt
}

func resolveRecordingPath(baseDir, filename string) (string, error) {
	if baseDir == "" {
		return "", fmt.Errorf("recording directory not configured")
	}
	cleanName := filepath.Clean(strings.TrimPrefix(filename, "/"))
	if cleanName == "." || cleanName == "" || strings.HasPrefix(cleanName, "..") {
		return "", fmt.Errorf("invalid filename")
	}

	fullPath := filepath.Join(baseDir, cleanName)
	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil {
		return "", fmt.Errorf("resolve filename: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid filename")
	}
	return fullPath, nil
}

func pruneEmptyRecordingDirs(baseDir, dir string) {
	baseDir = filepath.Clean(baseDir)
	dir = filepath.Clean(dir)
	for dir != baseDir && dir != "." && dir != string(filepath.Separator) {
		err := os.Remove(dir)
		if err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Shutdown stops all active workers (restreamers, recorders).
func (s *Server) Shutdown() {
	s.log.Info("API Server shutting down...")

	close(s.statsCacheStop)
	s.statsCacheWG.Wait()

	s.mu.Lock()
	restreamers := s.restreamers
	s.restreamers = make(map[string]*worker.Restreamer)
	recorders := s.recorders
	s.recorders = make(map[string]*worker.Recorder)
	s.mu.Unlock()

	for key, r := range restreamers {
		s.log.Info("Stopping restreamer", "key", key)
		r.Stop()
	}

	for key, r := range recorders {
		s.log.Info("Stopping recorder", "key", key)
		r.Stop()
	}

	if s.recWatch != nil {
		s.recWatch.Shutdown()
	}
	if s.recBroker != nil {
		s.recBroker.Shutdown()
	}

	s.log.Info("API Server shutdown complete")
}

func (s *Server) notifyRecordingsChanged() {
	if s.recBroker != nil {
		s.recBroker.Broadcast("update")
	}
}
