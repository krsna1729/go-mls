// Package api provides HTTP API handlers and routing
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"go-mls/internal/app"
	"go-mls/internal/errors"
	"go-mls/internal/httputil"
	"go-mls/internal/logger"
	"go-mls/internal/stream"
)

// Router handles all HTTP API routes
type Router struct {
	stream    *stream.StreamManager // Replaces RelayManager
	recording *stream.RecordingManager
	hls       *stream.HLSManager
	rtsp      *stream.RTSPServerManager
	logger    *logger.Logger
}

// NewRouter creates a new API router from an application context
func NewRouter(appCtx *app.Context) *Router {
	return &Router{
		stream:    appCtx.Stream,
		recording: appCtx.Recording,
		hls:       appCtx.HLS,
		rtsp:      appCtx.RTSP,
		logger:    appCtx.Logger,
	}
}

// RegisterRoutes registers all API routes with the given mux
func (rt *Router) RegisterRoutes(mux *http.ServeMux) {
	// Relay routes (now Stream routes)
	mux.HandleFunc("/api/relay/start", rt.handleStartRelay)
	mux.HandleFunc("/api/relay/stop", rt.handleStopRelay)
	mux.HandleFunc("/api/relay/status", rt.handleRelayStatus)
	mux.HandleFunc("/api/relay/export", rt.handleExportRelays)
	mux.HandleFunc("/api/relay/import", rt.handleImportRelays)
	mux.HandleFunc("/api/relay/presets", rt.handleRelayPresets)
	mux.HandleFunc("/api/relay/delete-input", rt.handleDeleteInput)
	mux.HandleFunc("/api/relay/delete-output", rt.handleDeleteOutput)

	// RTSP routes
	mux.HandleFunc("/api/rtsp/status", rt.handleRTSPStatus)

	// Recording routes
	mux.HandleFunc("/api/recording/start", stream.ApiStartRecording(rt.recording))
	mux.HandleFunc("/api/recording/stop", stream.ApiStopRecording(rt.recording))
	mux.HandleFunc("/api/recording/list", stream.ApiListRecordings(rt.recording))
	mux.HandleFunc("/api/recording/delete", stream.ApiDeleteRecording(rt.recording))
	mux.HandleFunc("/api/recording/download", stream.ApiDownloadRecording(rt.recording))
	mux.HandleFunc("/api/recording/sse", stream.ApiRecordingsSSE())

	// HLS routes
	// Note: ApiWatchInputHLS and others still need StreamManager for input relay coordination
	mux.HandleFunc("/api/relay/watch-input/hls/", stream.ApiWatchInputHLS(rt.hls, rt.stream))
	mux.HandleFunc("/api/relay/hls/start-viewer", stream.ApiStartHLSViewer(rt.hls, rt.stream))
	mux.HandleFunc("/api/relay/hls/stop-viewer", stream.ApiStopHLSViewer(rt.hls, rt.stream))
	mux.HandleFunc("/api/relay/hls/heartbeat", stream.ApiHLSViewerHeartbeat(rt.hls))

}

// writeError writes an AppError as JSON response
func (rt *Router) writeError(w http.ResponseWriter, err *errors.AppError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.StatusCode)
	json.NewEncoder(w).Encode(err)
}

// Relay handlers

func (rt *Router) handleStartRelay(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleStartRelay called")

	var req struct {
		InputURL       string            `json:"input_url"`
		OutputURL      string            `json:"output_url"`
		InputName      string            `json:"input_name"`
		OutputName     string            `json:"output_name"`
		PlatformPreset string            `json:"platform_preset"`
		FFmpegOptions  map[string]string `json:"ffmpeg_options"`
	}

	if err := httputil.DecodeJSON(r, &req); err != nil {
		rt.logger.Error("handleStartRelay: failed to decode request", "err", err)
		rt.writeError(w, errors.InvalidInput("Invalid request"))
		return
	}

	if req.InputName == "" || req.OutputName == "" {
		rt.logger.Error("handleStartRelay: missing required fields")
		rt.writeError(w, errors.InvalidInput("Input and output names are required"))
		return
	}

	rt.logger.Debug("handleStartRelay: starting relay",
		"inputURL", req.InputURL,
		"outputURL", req.OutputURL,
		"inputName", req.InputName,
		"outputName", req.OutputName,
		"preset", req.PlatformPreset)

	// Build FFmpeg options
	platformPreset := req.PlatformPreset
	var opts *stream.FFmpegOptions
	if req.FFmpegOptions != nil {
		opts = &stream.FFmpegOptions{
			VideoCodec: req.FFmpegOptions["video_codec"],
			AudioCodec: req.FFmpegOptions["audio_codec"],
			Resolution: req.FFmpegOptions["resolution"],
			Framerate:  req.FFmpegOptions["framerate"],
			Bitrate:    req.FFmpegOptions["bitrate"],
			Rotation:   req.FFmpegOptions["rotation"],
		}
	} else if platformPreset == "" {
		// Try to get stored configuration
		storedPreset, storedOpts, err := rt.stream.GetEndpointConfig(req.InputURL, req.OutputURL)
		if err == nil {
			platformPreset = storedPreset
			opts = storedOpts
			rt.logger.Debug("handleStartRelay: using stored config", "preset", platformPreset)
		}
	}

	// Call StartStream on StreamManager
	if err := rt.stream.StartStream(req.InputURL, req.OutputURL, req.InputName, req.OutputName, opts, platformPreset); err != nil {
		rt.logger.Error("handleStartRelay: failed to start relay", "err", err)
		rt.writeError(w, errors.Internal(err.Error()))
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
	rt.logger.Debug("handleStartRelay: relay started successfully")
}

func (rt *Router) handleStopRelay(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleStopRelay called")

	var req struct {
		InputURL   string `json:"input_url"`
		OutputURL  string `json:"output_url"`
		InputName  string `json:"input_name"`
		OutputName string `json:"output_name"`
	}

	if err := httputil.DecodeJSON(r, &req); err != nil {
		rt.logger.Error("handleStopRelay: failed to decode request", "err", err)
		rt.writeError(w, errors.InvalidInput("Invalid request"))
		return
	}

	if req.InputName == "" || req.OutputName == "" {
		rt.logger.Error("handleStopRelay: missing required fields")
		rt.writeError(w, errors.InvalidInput("Input and output names are required"))
		return
	}

	rt.logger.Debug("handleStopRelay: stopping relay",
		"inputURL", req.InputURL,
		"outputURL", req.OutputURL,
		"inputName", req.InputName,
		"outputName", req.OutputName)

	if err := rt.stream.StopStream(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
		rt.logger.Error("handleStopRelay: failed to stop relay", "err", err)
		rt.writeError(w, errors.Internal(err.Error()))
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	rt.logger.Debug("handleStopRelay: relay stopped successfully")
}

func (rt *Router) handleRelayStatus(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleRelayStatus called")
	// Returns the new unified StreamStatus
	httputil.WriteJSON(w, http.StatusOK, rt.stream.Status())
	rt.logger.Debug("handleRelayStatus: status returned")
}

func (rt *Router) handleExportRelays(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleExportRelays called")

	if err := rt.stream.ExportConfig("relay_config.json"); err != nil {
		rt.logger.Error("handleExportRelays: failed to export config", "err", err)
		rt.writeError(w, errors.Internal(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
	data, _ := os.ReadFile("relay_config.json")
	w.Write(data)
	rt.logger.Debug("handleExportRelays: config exported successfully")
}

func (rt *Router) handleImportRelays(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleImportRelays called")

	file, _, err := r.FormFile("file")
	if err != nil {
		rt.logger.Error("handleImportRelays: no file uploaded", "err", err)
		rt.writeError(w, errors.InvalidInput("No file uploaded"))
		return
	}
	defer file.Close()

	f, err := os.Create("relay_config.json")
	if err != nil {
		rt.logger.Error("handleImportRelays: failed to save file", "err", err)
		rt.writeError(w, errors.Internal("Failed to save file"))
		return
	}
	defer f.Close()

	io.Copy(f, file)

	if err := rt.stream.ImportConfig("relay_config.json"); err != nil {
		rt.logger.Error("handleImportRelays: failed to import config", "err", err)
		rt.writeError(w, errors.Internal(err.Error()))
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "imported"})
	rt.logger.Debug("handleImportRelays: config imported successfully")
}

func (rt *Router) handleRelayPresets(w http.ResponseWriter, r *http.Request) {
	presets := make(map[string]map[string]string)
	for name, preset := range stream.PlatformPresets {
		presets[name] = map[string]string{
			"video_codec": preset.Options.VideoCodec,
			"audio_codec": preset.Options.AudioCodec,
			"resolution":  preset.Options.Resolution,
			"framerate":   preset.Options.Framerate,
			"bitrate":     preset.Options.Bitrate,
			"rotation":    preset.Options.Rotation,
		}
	}
	httputil.WriteJSON(w, http.StatusOK, presets)
}

func (rt *Router) handleDeleteInput(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleDeleteInput called")

	var req struct {
		InputURL  string `json:"input_url"`
		InputName string `json:"input_name"`
	}

	if err := httputil.DecodeJSON(r, &req); err != nil {
		rt.logger.Error("handleDeleteInput: failed to decode request", "err", err)
		rt.writeError(w, errors.InvalidInput("Invalid request"))
		return
	}

	if req.InputName == "" {
		rt.logger.Error("handleDeleteInput: missing input name")
		rt.writeError(w, errors.InvalidInput("Input name is required"))
		return
	}

	rt.logger.Debug("handleDeleteInput: deleting input", "inputURL", req.InputURL, "inputName", req.InputName)
	if err := rt.stream.DeleteInput(req.InputURL, req.InputName); err != nil {
		rt.logger.Error("handleDeleteInput: failed to delete input", "err", err)
		rt.writeError(w, errors.Internal(err.Error()))
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	rt.logger.Debug("handleDeleteInput: input deleted successfully")
}

func (rt *Router) handleDeleteOutput(w http.ResponseWriter, r *http.Request) {
	rt.logger.Debug("handleDeleteOutput called")

	var req struct {
		InputURL   string `json:"input_url"`
		OutputURL  string `json:"output_url"`
		InputName  string `json:"input_name"`
		OutputName string `json:"output_name"`
	}

	if err := httputil.DecodeJSON(r, &req); err != nil {
		rt.logger.Error("handleDeleteOutput: failed to decode request", "err", err)
		rt.writeError(w, errors.InvalidInput("Invalid request"))
		return
	}

	if req.InputName == "" || req.OutputName == "" {
		rt.logger.Error("handleDeleteOutput: missing required fields")
		rt.writeError(w, errors.InvalidInput("Input and output names are required"))
		return
	}

	rt.logger.Debug("handleDeleteOutput: deleting output",
		"inputURL", req.InputURL,
		"outputURL", req.OutputURL,
		"inputName", req.InputName,
		"outputName", req.OutputName)

	if err := rt.stream.DeleteOutput(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
		rt.logger.Error("handleDeleteOutput: failed to delete output", "err", err)
		rt.writeError(w, errors.Internal(err.Error()))
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	rt.logger.Debug("handleDeleteOutput: output deleted successfully")
}

// RTSP handlers

func (rt *Router) handleRTSPStatus(w http.ResponseWriter, r *http.Request) {
	if rt.rtsp == nil {
		rt.writeError(w, errors.Internal("RTSP server not available"))
		return
	}

	stats := rt.rtsp.GetStreamStats()
	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"streams": stats,
		"total":   len(stats),
	})
}
