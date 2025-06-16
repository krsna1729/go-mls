package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"go-mls/internal/httputil"
	"go-mls/internal/logger"
	"go-mls/internal/stream"
)

//go:embed web/*
var webAssets embed.FS

func apiStartRelay(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiStartRelay called")
		var req struct {
			InputURL       string            `json:"input_url"`
			OutputURL      string            `json:"output_url"`
			InputName      string            `json:"input_name"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			relayMgr.Logger.Error("apiStartRelay: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiStartRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		relayMgr.Logger.Debug("apiStartRelay: starting relay for input=%s, output=%s, input_name=%s, output_name=%s, preset=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName, req.PlatformPreset)
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
		}
		if err := relayMgr.StartRelayWithOptions(req.InputURL, req.OutputURL, req.InputName, req.OutputName, opts, req.PlatformPreset); err != nil {
			relayMgr.Logger.Error("apiStartRelay: failed to start relay: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
		relayMgr.Logger.Debug("apiStartRelay: relay started successfully")
	}
}

func apiStopRelay(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiStopRelay called")
		var req struct {
			InputURL   string `json:"input_url"`
			OutputURL  string `json:"output_url"`
			InputName  string `json:"input_name"`
			OutputName string `json:"output_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			relayMgr.Logger.Error("apiStopRelay: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiStopRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		relayMgr.Logger.Debug("apiStopRelay: stopping relay for input=%s, output=%s, input_name=%s, output_name=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName)
		if err := relayMgr.StopRelay(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
			relayMgr.Logger.Error("apiStopRelay: failed to stop relay: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
		relayMgr.Logger.Debug("apiStopRelay: relay stopped successfully")
	}
}

func apiRelayStatus(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiRelayStatus called")
		httputil.WriteJSON(w, http.StatusOK, relayMgr.Status())
		relayMgr.Logger.Debug("apiRelayStatus: status returned")
	}
}

func apiExportRelays(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiExportRelays called")
		if err := relayMgr.ExportConfig("relay_config.json"); err != nil {
			relayMgr.Logger.Error("apiExportRelays: failed to export config: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
		data, _ := os.ReadFile("relay_config.json")
		w.Write(data)
		relayMgr.Logger.Debug("apiExportRelays: config exported successfully")
	}
}

func apiImportRelays(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiImportRelays called")
		file, _, err := r.FormFile("file")
		if err != nil {
			relayMgr.Logger.Error("apiImportRelays: no file uploaded: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "No file uploaded")
			return
		}
		defer file.Close()
		f, err := os.Create("relay_config.json")
		if err != nil {
			relayMgr.Logger.Error("apiImportRelays: failed to save file: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save file")
			return
		}
		defer f.Close()
		io.Copy(f, file)
		if err := relayMgr.ImportConfig("relay_config.json"); err != nil {
			relayMgr.Logger.Error("apiImportRelays: failed to import config: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "imported"})
		relayMgr.Logger.Debug("apiImportRelays: config imported successfully")
	}
}

func apiRelayPresets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
}

func main() {
	var recordingsDir string
	flag.StringVar(&recordingsDir, "recordings-dir", "./recordings", "Directory to store recordings")
	flag.Parse()

	logger := logger.NewLogger()

	absDir, err := filepath.Abs(recordingsDir)
	if err != nil {
		logger.Fatal("Failed to resolve recordings directory: %v", err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		logger.Fatal("Failed to create recordings directory: %v", err)
	}
	logger.Info("Using recordings directory: %s", absDir)

	relayMgr := stream.NewRelayManager(logger)
	recordingMgr := stream.NewRecordingManager(logger, absDir)

	// Use embedded static assets
	staticFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Error("Failed to create sub FS for web assets: %v", err)
		os.Exit(1)
	}
	fs := http.FileServer(http.FS(staticFS))
	http.Handle("/", fs)

	http.HandleFunc("/api/relay/start", apiStartRelay(relayMgr))
	http.HandleFunc("/api/relay/stop", apiStopRelay(relayMgr))
	http.HandleFunc("/api/relay/status", apiRelayStatus(relayMgr))
	http.HandleFunc("/api/relay/export", apiExportRelays(relayMgr))
	http.HandleFunc("/api/relay/import", apiImportRelays(relayMgr))
	http.HandleFunc("/api/relay/presets", apiRelayPresets())

	http.HandleFunc("/api/recording/start", stream.ApiStartRecording(recordingMgr))
	http.HandleFunc("/api/recording/stop", stream.ApiStopRecording(recordingMgr))
	http.HandleFunc("/api/recording/list", stream.ApiListRecordings(recordingMgr))
	http.HandleFunc("/api/recording/delete", stream.ApiDeleteRecording(recordingMgr))
	http.HandleFunc("/api/recording/download", stream.ApiDownloadRecording(recordingMgr))
	http.HandleFunc("/api/recording/sse", stream.ApiRecordingsSSE())

	logger.Info("Go-MLS relay manager running at http://localhost:8080 ...")
	logger.Debug("main: server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Error("Server error: %v", err)
	}
	logger.Debug("main: server shutdown")
}
