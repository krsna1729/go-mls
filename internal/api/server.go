// Package api implements the HTTP Control Plane for go-mls.
// It exposes all REST endpoints defined in the REFACTOR.md spec.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

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

	// Active workers tracking
	mu          sync.RWMutex
	restreamers map[string]*worker.Restreamer // keyed by stream_path/output_id
	recorders   map[string]*worker.Recorder   // keyed by stream_path
}

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
	return &Server{
		store:       store,
		ingest:      ingestRouter,
		hlsMgr:      hlsMgr,
		log:         log.With("component", "api"),
		recDir:      recDir,
		hlsDir:      hlsDir,
		rtmpPort:    rtmpPort,
		configPath:  configPath,
		ctx:         ctx,
		restreamers: make(map[string]*worker.Restreamer),
		recorders:   make(map[string]*worker.Recorder),
	}
}

// RegisterRoutes registers all API endpoints on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Core REFACTOR.md endpoints
	mux.HandleFunc("/inputs", s.handleInputs)
	mux.HandleFunc("/outputs", s.handleOutputs)
	mux.HandleFunc("/presets", s.handlePresets)
	mux.HandleFunc("/record", s.handleRecord)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/system/export", s.handleExport)
	mux.HandleFunc("/system/import", s.handleImport)

	// HLS endpoints
	mux.HandleFunc("/hls/start", s.handleHLSStart)
	mux.HandleFunc("/hls/stop", s.handleHLSStop)

	// HLS file serving - serve from hls directory
	if s.hlsDir != "" {
		hlsFS := http.FileServer(http.Dir(s.hlsDir))
		mux.Handle("/hls/", http.StripPrefix("/hls/", hlsFS))
	}

	// Recordings file serving - serve from recordings directory
	if s.recDir != "" {
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
			rs.Stop()
			delete(s.restreamers, key)
		}
	}

	// Stop recording if active
	if rec, ok := s.recorders[streamPath]; ok {
		rec.Stop()
		delete(s.recorders, streamPath)
		s.store.RemoveRecording(streamPath)
	}
	s.mu.Unlock()

	if err := s.ingest.UnregisterInput(streamPath); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	s.log.Info("Input removed", "stream_path", streamPath)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listInputs(w http.ResponseWriter, r *http.Request) {
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
	VideoArgs  []string `json:"video_args,omitempty"`
	AudioArgs  []string `json:"audio_args,omitempty"`
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

	if req.Preset != "" || req.VideoCodec != "" || req.AudioCodec != "" || req.Resolution != "" || req.Framerate != "" || req.Bitrate != "" {
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

		if len(videoArgs) == 0 {
			videoArgs = presetVideoArgs
		}
		if len(audioArgs) == 0 {
			audioArgs = presetAudioArgs
		}
	}

	out := &state.Output{
		StreamPath: req.StreamPath,
		OutputID:   req.OutputID,
		RemoteURL:  req.RemoteURL,
		StreamKey:  req.StreamKey,
		VideoArgs:  videoArgs,
		AudioArgs:  audioArgs,
		Status:     state.OutputStatusStarting,
	}

	if err := s.store.AddOutput(out); err != nil {
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
		rs.Stop()
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

	resp := statsResponse{
		Inputs:  make([]inputStats, 0),
		Outputs: make([]outputStats, 0),
	}

	// Server-level stats (self process CPU and memory)
	resp.Server = getSelfStats()

	// Input stats
	for _, in := range s.store.ListInputs() {
		is := inputStats{
			StreamPath: in.StreamPath,
			Mode:       in.Mode,
			Status:     in.Status,
			RemoteURL:  in.RemoteURL,
			LastError:  in.LastError,
		}
		if in.PID > 0 {
			if t, ok := s.store.GetTelemetry(in.PID); ok {
				is.Telemetry = t
			}
		}
		resp.Inputs = append(resp.Inputs, is)
	}

	// Output stats
	for _, out := range s.store.ListOutputs() {
		os := outputStats{
			StreamPath: out.StreamPath,
			OutputID:   out.OutputID,
			RemoteURL:  out.RemoteURL,
			Status:     out.Status,
			LastError:  out.LastError,
		}
		if out.PID > 0 {
			if t, ok := s.store.GetTelemetry(out.PID); ok {
				os.Telemetry = t
			} else {
				os.Telemetry = getProcessTelemetry(out.PID)
			}
		}
		resp.Outputs = append(resp.Outputs, os)
	}

	writeJSON(w, http.StatusOK, resp)
}

// getSelfStats returns CPU and memory usage for the current process
func getSelfStats() serverStats {
	stats := serverStats{}
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return stats
	}

	if cpu, err := p.CPUPercent(); err == nil {
		stats.CPU = cpu
	}
	if mem, err := p.MemoryInfo(); err == nil {
		stats.Mem = float64(mem.RSS) / (1024 * 1024) // Convert to MB
	}

	return stats
}

// getProcessTelemetry returns real-time CPU/memory for a given PID
func getProcessTelemetry(pid int) *state.Telemetry {
	t := &state.Telemetry{}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return t
	}
	if cpu, err := p.CPUPercent(); err == nil {
		t.CPU = cpu
	}
	if mem, err := p.MemoryInfo(); err == nil {
		t.MemMB = float64(mem.RSS) / (1024 * 1024)
	}
	return t
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
			OutputURL:  out.RemoteURL,
			OutputName: out.OutputID,
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

	// Stop all current workers
	s.mu.Lock()
	for key, rs := range s.restreamers {
		rs.Stop()
		delete(s.restreamers, key)
	}
	for key, rec := range s.recorders {
		rec.Stop()
		delete(s.recorders, key)
	}
	s.mu.Unlock()

	var totalInputs, totalOutputs int

	for _, relay := range relays {
		// Create input - if input_url is empty, treat as accepting push
		in := &state.Input{
			StreamPath: relay.InputName,
			RemoteURL:  relay.InputURL,
		}
		if relay.InputURL == "" {
			in.Mode = state.InputModeAccept
		} else {
			in.Mode = state.InputModePull
		}
		if err := s.store.AddInput(in); err != nil {
			s.log.Error("Failed to add input", "input_name", relay.InputName, "error", err)
			continue
		}
		totalInputs++

		// Create outputs
		for _, out := range relay.Outputs {
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
				StreamPath: relay.InputName,
				OutputID:   out.OutputName,
				RemoteURL:  out.OutputURL,
				VideoArgs:  videoArgs,
				AudioArgs:  audioArgs,
			}

			if err := s.store.AddOutput(stateOut); err != nil {
				s.log.Error("Failed to add output", "output_name", out.OutputName, "error", err)
				continue
			}

			rs, err := worker.StartRestreamer(s.ctx, s.store, s.log, stateOut, s.rtmpPort)
			if err != nil {
				s.store.RemoveOutput(relay.InputName, out.OutputName)
				s.log.Error("Failed to start restreamer", "output_id", out.OutputName, "error", err)
				continue
			}

			key := relay.InputName + "/" + out.OutputName
			s.mu.Lock()
			s.restreamers[key] = rs
			s.mu.Unlock()
			totalOutputs++
		}
	}

	s.log.Info("Configuration imported", "inputs", totalInputs, "outputs", totalOutputs)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

	playlistURL, err := s.hlsMgr.AddViewer(s.ctx, streamPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	viewerID := streamPath
	s.log.Info("HLS viewer started", "stream_path", streamPath)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "stream_path": streamPath, "viewer_id": viewerID, "playlist_url": playlistURL})
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

	if req.Stream == "" && req.ViewerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream or viewer_id required"})
		return
	}

	streamPath := req.Stream
	if streamPath == "" {
		streamPath = req.ViewerID
	}

	s.hlsMgr.RemoveViewer(streamPath)
	s.log.Info("HLS viewer stopped", "stream_path", streamPath)
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

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Shutdown stops all active workers (restreamers, recorders).
func (s *Server) Shutdown() {
	s.log.Info("API Server shutting down...")

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

	s.log.Info("API Server shutdown complete")
}
