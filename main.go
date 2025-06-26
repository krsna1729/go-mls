package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"go-mls/internal/config"
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

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("apiStartRelay: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		// Validate required fields
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiStartRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}

		relayMgr.Logger.Debug("apiStartRelay: starting relay for input=%s, output=%s, input_name=%s, output_name=%s, preset=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName, req.PlatformPreset)

		// Check if preset/options are provided in request, otherwise try to get from stored config
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
			// Try to get stored configuration for this endpoint
			storedPreset, storedOpts, err := relayMgr.GetEndpointConfig(req.InputURL, req.OutputURL)
			if err == nil {
				platformPreset = storedPreset
				opts = storedOpts
				relayMgr.Logger.Debug("apiStartRelay: using stored config - preset=%s, options=%+v", platformPreset, opts)
			}
		}
		if err := relayMgr.StartRelayWithOptions(req.InputURL, req.OutputURL, req.InputName, req.OutputName, opts, platformPreset); err != nil {
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

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
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
		httputil.WriteJSON(w, http.StatusOK, relayMgr.StatusV2())
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

func apiRTSPStatus(rtspServer *stream.RTSPServerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rtspServer == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "RTSP server not available")
			return
		}
		stats := rtspServer.GetStreamStats()
		httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"streams": stats,
			"total":   len(stats),
		})
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

func apiDeleteInput(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiDeleteInput called")
		var req struct {
			InputURL  string `json:"input_url"`
			InputName string `json:"input_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("apiDeleteInput: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" {
			relayMgr.Logger.Error("apiDeleteInput: missing input name")
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}
		relayMgr.Logger.Debug("apiDeleteInput: deleting input for input=%s, input_name=%s", req.InputURL, req.InputName)
		if err := relayMgr.DeleteInput(req.InputURL, req.InputName); err != nil {
			relayMgr.Logger.Error("apiDeleteInput: failed to delete input: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		relayMgr.Logger.Debug("apiDeleteInput: input deleted successfully")
	}
}

func apiDeleteOutput(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiDeleteOutput called")
		var req struct {
			InputURL   string `json:"input_url"`
			OutputURL  string `json:"output_url"`
			InputName  string `json:"input_name"`
			OutputName string `json:"output_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("apiDeleteOutput: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiDeleteOutput: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		relayMgr.Logger.Debug("apiDeleteOutput: deleting output for input=%s, output=%s, input_name=%s, output_name=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName)
		if err := relayMgr.DeleteOutput(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
			relayMgr.Logger.Error("apiDeleteOutput: failed to delete output: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		relayMgr.Logger.Debug("apiDeleteOutput: output deleted successfully")
	}
}

// apiWatchInputHLS handles HLS playlist/segment requests for a given input relay.
func apiWatchInputHLS(hlsMgr *stream.HLSManager, relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// URL: /api/relay/watch-input/hls/{inputName}/{file}
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/relay/watch-input/hls/"), "/", 2)
		if len(parts) != 2 {
			relayMgr.Logger.Error("Invalid HLS request path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		inputName, file := parts[0], parts[1]
		if inputName == "" || file == "" {
			relayMgr.Logger.Error("Missing inputName or file in HLS request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// HLS manager will handle starting input relay if needed
		hlsMgr.ServeHLS(w, r, inputName, file, "")
	}
}

// apiStartHLSViewer creates a new HLS viewer session
func apiStartHLSViewer(hlsMgr *stream.HLSManager, relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("HLS start viewer: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" {
			relayMgr.Logger.Error("HLS start viewer: missing input name")
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}

		// HLS manager will handle starting input relay if needed
		viewerID, err := hlsMgr.AddViewer(req.InputName, "")
		if err != nil {
			relayMgr.Logger.Error("HLS start viewer: failed to add viewer for input %s: %v", req.InputName, err)
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to start HLS viewer")
			return
		}

		relayMgr.Logger.Info("HLS viewer started: input=%s, viewerID=%s", req.InputName, viewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{
			"viewer_id":    viewerID,
			"playlist_url": fmt.Sprintf("/api/relay/watch-input/hls/%s/index.m3u8", req.InputName),
		})
	}
}

// apiStopHLSViewer stops an HLS viewer session
func apiStopHLSViewer(hlsMgr *stream.HLSManager, relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
			ViewerID  string `json:"viewer_id"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("HLS stop viewer: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" || req.ViewerID == "" {
			relayMgr.Logger.Error("HLS stop viewer: missing input name or viewer ID")
			httputil.WriteError(w, http.StatusBadRequest, "Input name and viewer ID are required")
			return
		}

		hlsMgr.RemoveViewer(req.InputName, req.ViewerID)
		relayMgr.Logger.Info("HLS viewer stopped: input=%s, viewerID=%s", req.InputName, req.ViewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// apiHLSViewerHeartbeat updates viewer heartbeat
func apiHLSViewerHeartbeat(hlsMgr *stream.HLSManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
			ViewerID  string `json:"viewer_id"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" || req.ViewerID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Input name and viewer ID are required")
			return
		}

		hlsMgr.UpdateViewerHeartbeat(req.InputName, req.ViewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func main() {
	var configFile string
	var recordingsDir string
	flag.StringVar(&configFile, "config", "config.json", "Configuration file path")
	flag.StringVar(&recordingsDir, "recordings-dir", "", "Directory to store recordings (overrides config)")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Override recordings directory if provided via command line
	if recordingsDir != "" {
		cfg.Recording.Directory = recordingsDir
	}

	logger := logger.NewLogger()
	logger.Info("Starting Go-MLS Relay Manager")

	// Get initial goroutine count
	initialGoroutines := runtime.NumGoroutine()

	absDir, err := filepath.Abs(cfg.Recording.Directory)
	if err != nil {
		logger.Fatal("Failed to resolve recordings directory: %v", err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		logger.Fatal("Failed to create recordings directory: %v", err)
	}
	logger.Info("Using recordings directory: %s", absDir)

	// Initialize RTSP server with configuration
	rtspServer := stream.NewRTSPServerManager(logger)
	// TODO: Use RTSP configuration from config file
	if err := rtspServer.Start(); err != nil {
		logger.Fatal("Failed to start RTSP server: %v", err)
	}

	relayMgr := stream.NewRelayManager(logger, absDir)
	relayMgr.SetRTSPServer(rtspServer)
	// Set relay configuration timeouts
	relayMgr.SetTimeouts(cfg.Relay.InputTimeout, cfg.Relay.OutputTimeout)

	recordingMgr := stream.NewRecordingManager(logger, absDir, relayMgr)

	// Instantiate HLSManager (ffmpeg path, cleanup interval, session timeout)
	hlsMgr := stream.NewHLSManager("ffmpeg", 2*time.Minute, 5*time.Minute)
	// Connect HLS manager to relay manager for proper consumer management
	hlsMgr.SetRelayManager(relayMgr)

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
	http.HandleFunc("/api/relay/delete-input", apiDeleteInput(relayMgr))
	http.HandleFunc("/api/relay/delete-output", apiDeleteOutput(relayMgr))
	http.HandleFunc("/api/relay/status", apiRelayStatus(relayMgr))
	http.HandleFunc("/api/relay/export", apiExportRelays(relayMgr))
	http.HandleFunc("/api/relay/import", apiImportRelays(relayMgr))
	http.HandleFunc("/api/relay/presets", apiRelayPresets())
	http.HandleFunc("/api/rtsp/status", apiRTSPStatus(rtspServer))

	http.HandleFunc("/api/recording/start", stream.ApiStartRecording(recordingMgr))
	http.HandleFunc("/api/recording/stop", stream.ApiStopRecording(recordingMgr))
	http.HandleFunc("/api/recording/list", stream.ApiListRecordings(recordingMgr))
	http.HandleFunc("/api/recording/delete", stream.ApiDeleteRecording(recordingMgr))
	http.HandleFunc("/api/recording/download", stream.ApiDownloadRecording(recordingMgr))
	http.HandleFunc("/api/recording/sse", stream.ApiRecordingsSSE())

	http.HandleFunc("/api/input/delete", apiDeleteInput(relayMgr))
	http.HandleFunc("/api/output/delete", apiDeleteOutput(relayMgr))
	http.HandleFunc("/api/relay/watch-input/hls/", apiWatchInputHLS(hlsMgr, relayMgr))
	http.HandleFunc("/api/relay/hls/start-viewer", apiStartHLSViewer(hlsMgr, relayMgr))
	http.HandleFunc("/api/relay/hls/stop-viewer", apiStopHLSViewer(hlsMgr, relayMgr))
	http.HandleFunc("/api/relay/hls/heartbeat", apiHLSViewerHeartbeat(hlsMgr))

	// Create HTTP server with proper shutdown support and timeout configuration
	server := &http.Server{
		Addr: cfg.HTTP.Host + ":" + cfg.HTTP.Port,

		// Connection timeouts from configuration
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout, // Important for SSE connections
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ReadHeaderTimeout: 5 * time.Second, // Keep fixed for security

		// Maximum header size (default 1MB is usually fine)
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	// Channel to listen for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		logger.Info("Go-MLS relay manager running at http://%s:%s ...", cfg.HTTP.Host, cfg.HTTP.Port)
		logger.Debug("main: server starting on %s:%s", cfg.HTTP.Host, cfg.HTTP.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	logger.Info("Received interrupt signal, initiating graceful shutdown...")

	// Write endlist to all HLS sessions
	logger.Info("Signalling stream end to all HLS sessions...")
	hlsMgr.WriteEndlistToAll()
	// Give clients a moment to fetch the final playlist
	time.Sleep(15 * time.Second)

	// Create a context with timeout for graceful shutdown
	// Increased timeout to allow SSE connections and long-running requests to close properly
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server
	logger.Info("Shutting down HTTP server...")
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server shutdown error: %v", err)
	}

	// Shutdown HLS manager and clean up all HLS sessions/ffmpeg processes
	logger.Info("Shutting down HLS manager...")
	hlsMgr.Shutdown()

	// Stop all recordings and shut down recording manager
	logger.Info("Shutting down recording manager...")
	recordingMgr.Shutdown()

	// Stop all active relays
	logger.Info("Stopping all active relays...")
	relayMgr.StopAllRelays()

	// Stop RTSP server
	logger.Info("Stopping RTSP server...")
	rtspServer.Stop()

	// Give more time for cleanup of goroutines
	logger.Info("Waiting for goroutines to clean up...")
	time.Sleep(3 * time.Second)

	// Print resource usage statistics
	printResourceUsage(logger, initialGoroutines)

	logger.Info("Application shutdown complete")
}

// dumpGoroutineProfiles provides detailed goroutine analysis for leak detection
func dumpGoroutineProfiles(logger *logger.Logger) {
	logger.Info("=== Goroutine Leak Analysis ===")

	// Use runtime stack trace for more reliable parsing
	stack := make([]byte, 1<<16) // 64KB buffer
	n := runtime.Stack(stack, true)
	stackStr := string(stack[:n])

	// Parse goroutines from stack trace
	goroutines := strings.Split(stackStr, "\n\ngoroutine ")

	systemGoroutines := 0
	applicationGoroutines := 0

	// Known system/expected goroutines that are not leaks
	systemPatterns := []string{
		"os/signal.loop",                        // Signal handler
		"os/signal.signal_recv",                 // Signal receiver
		"signal_recv",                           // Signal receiver alternate
		"runtime.gopark",                        // Runtime parking
		"runtime.(*gcBgMarkWorker)",             // GC background worker
		"net/http.(*conn).serve",                // HTTP connection handler
		"net/http.(*connReader).backgroundRead", // HTTP background reader
		"internal/poll.runtime_pollWait",        // Network I/O wait
		"net.(*netFD).Read",                     // Network read
		"created by os/signal.Notify",           // Signal notification setup
	}

	logger.Info("Active goroutines by category:")

	totalGoroutines := 0

	for i, goroutine := range goroutines {
		if strings.TrimSpace(goroutine) == "" {
			continue
		}

		totalGoroutines++

		// For the first goroutine, it doesn't have the "goroutine " prefix stripped
		var goroutineInfo string
		if i == 0 {
			lines := strings.Split(goroutine, "\n")
			if len(lines) > 0 && strings.HasPrefix(lines[0], "goroutine ") {
				goroutineInfo = lines[0]
			} else {
				continue // Skip if not a proper goroutine
			}
		} else {
			// Add back the "goroutine " prefix
			lines := strings.Split(goroutine, "\n")
			if len(lines) > 0 {
				goroutineInfo = "goroutine " + lines[0]
			} else {
				continue
			}
		}

		// Check if this is a system/expected goroutine
		isSystemGoroutine := false
		for _, pattern := range systemPatterns {
			if strings.Contains(goroutine, pattern) {
				isSystemGoroutine = true
				systemGoroutines++
				break
			}
		}

		if isSystemGoroutine {
			logger.Info("  [SYSTEM] %s", goroutineInfo)
		} else {
			applicationGoroutines++
			logger.Info("  [APP] %s", goroutineInfo)
			// Show first few lines of stack trace for application goroutines
			lines := strings.Split(goroutine, "\n")
			for j := 1; j < len(lines) && j < 4; j++ {
				if strings.TrimSpace(lines[j]) != "" {
					logger.Info("    └─ %s", strings.TrimSpace(lines[j]))
				}
			}
		}
	}

	logger.Info("Goroutine Summary:")
	logger.Info("  Total: %d", totalGoroutines)
	logger.Info("  System/Expected: %d", systemGoroutines)
	logger.Info("  Application: %d", applicationGoroutines)

	// Also dump simplified stack trace for debugging if needed
	if applicationGoroutines > 0 {
		logger.Info("\n=== Full Stack Trace (last 50 lines) ===")
		stackLines := strings.Split(stackStr, "\n")

		// Show last 50 lines to avoid overwhelming output
		start := len(stackLines) - 50
		if start < 0 {
			start = 0
		}
		for i := start; i < len(stackLines); i++ {
			logger.Info("%s", stackLines[i])
		}
	}

	logger.Info("===============================")
}

// printResourceUsage prints current resource usage statistics
func printResourceUsage(logger *logger.Logger, initialGoroutines int) {
	// Get current goroutine count
	currentGoroutines := runtime.NumGoroutine()

	// Get memory statistics
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	logger.Info("=== Resource Usage Report ===")
	logger.Info("Goroutines:")
	logger.Info("  Initial: %d", initialGoroutines)
	logger.Info("  Current: %d", currentGoroutines)
	logger.Info("  Difference: %+d", currentGoroutines-initialGoroutines)

	if currentGoroutines > initialGoroutines {
		logger.Warn("WARNING: %d goroutines may have leaked!", currentGoroutines-initialGoroutines)
		dumpGoroutineProfiles(logger)
	} else {
		logger.Info("✓ No goroutine leaks detected")
	}

	logger.Info("Memory Usage:")
	logger.Info("  Allocated: %s", formatBytes(memStats.Alloc))
	logger.Info("  Total Allocations: %s", formatBytes(memStats.TotalAlloc))
	logger.Info("  System Memory: %s", formatBytes(memStats.Sys))
	logger.Info("  GC Cycles: %d", memStats.NumGC)
	logger.Info("  Heap Objects: %d", memStats.HeapObjects)

	logger.Info("System Info:")
	logger.Info("  CPU Cores: %d", runtime.NumCPU())
	logger.Info("  Go Version: %s", runtime.Version())
	logger.Info("  OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)

	logger.Info("==============================")
}

// formatBytes converts bytes to human readable format
func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
