// Package api implements the HTTP Control Plane for go-mls.
// It exposes all REST endpoints defined in the REFACTOR.md spec.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"go-mls/internal/ingest"
	"go-mls/internal/logger"
	"go-mls/internal/state"
	"go-mls/internal/worker"
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

	out := &state.Output{
		StreamPath: req.StreamPath,
		OutputID:   req.OutputID,
		RemoteURL:  req.RemoteURL,
		StreamKey:  req.StreamKey,
		VideoArgs:  req.VideoArgs,
		AudioArgs:  req.AudioArgs,
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

	// Server-level stats (self process)
	// NOTE: gopsutil self-usage can be added here
	resp.Server = serverStats{}

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
			}
		}
		resp.Outputs = append(resp.Outputs, os)
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- /system/export ---

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snap := s.store.TakeSnapshot()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"config.json\"")
	w.Write(data)
}

// --- /system/import ---

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var snap state.Snapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
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

	// Load the snapshot
	s.store.LoadSnapshot(&snap)

	// Resume inputs
	for _, in := range snap.Inputs {
		if err := s.ingest.RegisterInput(s.ctx, in); err != nil {
			s.log.Error("Failed to resume input", "stream_path", in.StreamPath, "error", err)
		}
	}

	// Resume outputs
	s.mu.Lock()
	for _, out := range snap.Outputs {
		rs, err := worker.StartRestreamer(s.ctx, s.store, s.log, out, s.rtmpPort)
		if err != nil {
			s.log.Error("Failed to resume output", "output_id", out.OutputID, "error", err)
			continue
		}
		key := out.StreamPath + "/" + out.OutputID
		s.restreamers[key] = rs
	}
	s.mu.Unlock()

	s.log.Info("Configuration imported", "inputs", len(snap.Inputs), "outputs", len(snap.Outputs))
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
