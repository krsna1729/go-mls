package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
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
	"go-mls/internal/logger"
	"go-mls/internal/stream"
)

//go:embed web/*
var webAssets embed.FS

func main() {
	var configFile string
	var recordingsDir string
	flag.StringVar(&configFile, "config", "config.json", "Configuration file path")
	flag.StringVar(&recordingsDir, "recordings-dir", "", "Directory to store recordings (overrides config)")
	flag.Parse()

	// Create a temporary logger for config loading
	tempLogger := logger.NewLogger()

	// Load configuration
	cfg, err := config.LoadConfig(configFile, tempLogger)
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Override recordings directory if provided via command line
	if recordingsDir != "" {
		cfg.Recording.Directory = recordingsDir
	}

	// Use config for logger
	logger := logger.NewLoggerWithConfig(cfg.Logging.Level, cfg.Logging.File)
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
	rtspServer := stream.NewRTSPServerManager(logger, cfg.Relay.RTSPServer.Host, cfg.Relay.RTSPServer.Port)
	if err := rtspServer.Start(); err != nil {
		logger.Fatal("Failed to start RTSP server: %v", err)
	}

	relayMgr := stream.NewRelayManager(logger, absDir, cfg.FFmpeg.LogLevel)
	relayMgr.SetRTSPServer(rtspServer)
	// Set relay configuration timeouts
	relayMgr.SetTimeouts(time.Duration(cfg.Relay.InputTimeout), time.Duration(cfg.Relay.OutputTimeout))

	recordingMgr := stream.NewRecordingManager(logger, absDir, relayMgr)

	// Convert config.HLSConfig durations to time.Duration for HLSManager
	hlsMgr := stream.NewHLSManager(stream.HLSManagerConfig{
		CleanupInterval:        time.Duration(cfg.HLS.CleanupInterval),
		SessionTimeout:         time.Duration(cfg.HLS.SessionTimeout),
		FailedCooldown:         time.Duration(cfg.HLS.FailedCooldown),
		PlaylistReadyTimeout:   time.Duration(cfg.HLS.PlaylistReadyTimeout),
		PlaylistPollInterval:   time.Duration(cfg.HLS.PlaylistPollInterval),
		PlaylistPollAttempts:   cfg.HLS.PlaylistPollAttempts,
		ViewerHeartbeatTimeout: time.Duration(cfg.HLS.ViewerHeartbeatTimeout),
		FFmpegStopTimeout:      time.Duration(cfg.HLS.FFmpegStopTimeout),
		PlaylistBaseDir:        cfg.HLS.PlaylistBaseDir,
	}, logger)

	// Wire up references for proper integration
	relayMgr.SetHLSManager(hlsMgr)
	relayMgr.SetRecordingManager(recordingMgr)
	hlsMgr.SetRelayManager(relayMgr)

	// Use embedded static assets
	staticFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Error("Failed to create sub FS for web assets: %v", err)
		os.Exit(1)
	}
	fs := http.FileServer(http.FS(staticFS))
	http.Handle("/", fs)

	// API Routes - using organized handlers from stream package
	http.HandleFunc("/api/relay/start", stream.ApiStartRelay(relayMgr))
	http.HandleFunc("/api/relay/stop", stream.ApiStopRelay(relayMgr))
	http.HandleFunc("/api/relay/delete-input", stream.ApiDeleteInput(relayMgr))
	http.HandleFunc("/api/relay/delete-output", stream.ApiDeleteOutput(relayMgr))
	http.HandleFunc("/api/relay/status", stream.ApiRelayStatus(relayMgr))
	http.HandleFunc("/api/relay/export", stream.ApiExportRelays(relayMgr))
	http.HandleFunc("/api/relay/import", stream.ApiImportRelays(relayMgr))
	http.HandleFunc("/api/relay/presets", stream.ApiRelayPresets())
	http.HandleFunc("/api/rtsp/status", stream.ApiRTSPStatus(rtspServer))

	http.HandleFunc("/api/recording/start", stream.ApiStartRecording(recordingMgr))
	http.HandleFunc("/api/recording/stop", stream.ApiStopRecording(recordingMgr))
	http.HandleFunc("/api/recording/list", stream.ApiListRecordings(recordingMgr))
	http.HandleFunc("/api/recording/delete", stream.ApiDeleteRecording(recordingMgr))
	http.HandleFunc("/api/recording/download", stream.ApiDownloadRecording(recordingMgr))
	http.HandleFunc("/api/recording/sse", stream.ApiRecordingsSSE())

	http.HandleFunc("/api/input/delete", stream.ApiDeleteInput(relayMgr))
	http.HandleFunc("/api/output/delete", stream.ApiDeleteOutput(relayMgr))
	http.HandleFunc("/api/relay/watch-input/hls/", stream.ApiWatchInputHLS(hlsMgr, relayMgr))
	http.HandleFunc("/api/relay/hls/start-viewer", stream.ApiStartHLSViewer(hlsMgr, relayMgr))
	http.HandleFunc("/api/relay/hls/stop-viewer", stream.ApiStopHLSViewer(hlsMgr, relayMgr))
	http.HandleFunc("/api/relay/hls/heartbeat", stream.ApiHLSViewerHeartbeat(hlsMgr))

	// Create HTTP server with proper shutdown support and timeout configuration
	server := &http.Server{
		Addr: cfg.HTTP.Host + ":" + cfg.HTTP.Port,

		// Connection timeouts from configuration
		ReadTimeout:       time.Duration(cfg.HTTP.ReadTimeout),
		WriteTimeout:      time.Duration(cfg.HTTP.WriteTimeout), // Important for SSE connections
		IdleTimeout:       time.Duration(cfg.HTTP.IdleTimeout),
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
