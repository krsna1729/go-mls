package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"go-mls/internal/api"
	"go-mls/internal/app"
	"go-mls/internal/config"
	"go-mls/internal/logger"
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
		tempLogger.Error("Failed to load configuration", "err", err)
		os.Exit(1)
	}

	// Override recordings directory if provided via command line
	if recordingsDir != "" {
		cfg.Recording.Directory = recordingsDir
	}

	// Use config for logger
	log := logger.NewLoggerWithConfig(cfg.Logging.Level, cfg.Logging.File)
	log.Info("Starting Go-MLS Relay Manager")

	// Get initial goroutine count for leak detection
	initialGoroutines := runtime.NumGoroutine()

	// Resolve and create recordings directory
	absDir, err := filepath.Abs(cfg.Recording.Directory)
	if err != nil {
		log.Fatal("Failed to resolve recordings directory", "err", err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		log.Fatal("Failed to create recordings directory", "err", err)
	}
	log.Info("Using recordings directory", "dir", absDir)

	// Update config with absolute path
	cfg.Recording.Directory = absDir

	// Initialize application context
	appCtx, err := app.NewContext(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize application context", "err", err)
	}

	// Start all components
	if err := appCtx.Start(); err != nil {
		log.Fatal("Failed to start application", "err", err)
	}

	// Create API router
	router := api.NewRouter(appCtx)

	// Use embedded static assets
	staticFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Error("Failed to create sub FS for web assets", "err", err)
		os.Exit(1)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	http.Handle("/", fileServer)

	// Register API routes
	router.RegisterRoutes(http.DefaultServeMux)

	// Create HTTP server with proper configuration
	server := &http.Server{
		Addr:              cfg.HTTP.Host + ":" + cfg.HTTP.Port,
		ReadTimeout:       time.Duration(cfg.HTTP.ReadTimeout),
		WriteTimeout:      time.Duration(cfg.HTTP.WriteTimeout),
		IdleTimeout:       time.Duration(cfg.HTTP.IdleTimeout),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// Channel to listen for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		log.Info("Go-MLS relay manager running", "host", cfg.HTTP.Host, "port", cfg.HTTP.Port)
		log.Debug("main: server starting", "host", cfg.HTTP.Host, "port", cfg.HTTP.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Server error", "err", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Info("Received interrupt signal, initiating graceful shutdown...")

	// Create a context with timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server
	log.Info("Shutting down HTTP server...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("Server shutdown error", "err", err)
	}

	// Shutdown application context (HLS, recordings, relays, RTSP)
	appCtx.Shutdown()

	// Print resource usage statistics
	printResourceUsage(log, initialGoroutines)

	log.Info("Application shutdown complete")
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
			logger.Info("System goroutine", "info", goroutineInfo)
		} else {
			applicationGoroutines++
			logger.Info("Application goroutine", "info", goroutineInfo)
			// Show first few lines of stack trace for application goroutines
			lines := strings.Split(goroutine, "\n")
			for j := 1; j < len(lines) && j < 4; j++ {
				if strings.TrimSpace(lines[j]) != "" {
					logger.Info("App goroutine stack", "line", strings.TrimSpace(lines[j]))
				}
			}
		}
	}

	logger.Info("Goroutine summary", "total", totalGoroutines, "system", systemGoroutines, "application", applicationGoroutines)

	// Also dump simplified stack trace for debugging if needed
	if applicationGoroutines > 0 {
		logger.Info("Full stack trace (last 50 lines)")
		stackLines := strings.Split(stackStr, "\n")
		start := len(stackLines) - 50
		if start < 0 {
			start = 0
		}
		for i := start; i < len(stackLines); i++ {
			logger.Info("Stack line", "line", stackLines[i])
		}
	}

	logger.Info("==============================")
}

// printResourceUsage prints current resource usage statistics
func printResourceUsage(logger *logger.Logger, initialGoroutines int) {
	// Get current goroutine count
	currentGoroutines := runtime.NumGoroutine()

	// Get memory statistics
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	logger.Info("=== Resource Usage Report ===")
	logger.Info("Goroutine counts", "initial", initialGoroutines, "current", currentGoroutines, "difference", currentGoroutines-initialGoroutines)

	if currentGoroutines > initialGoroutines {
		logger.Warn("Goroutines may have leaked!", "leaked", currentGoroutines-initialGoroutines)
		dumpGoroutineProfiles(logger)
	} else {
		logger.Info("No goroutine leaks detected")
	}

	logger.Info("Memory usage", "allocated_bytes", memStats.Alloc, "total_alloc_bytes", memStats.TotalAlloc, "system_bytes", memStats.Sys, "gc_cycles", memStats.NumGC, "heap_objects", memStats.HeapObjects)

	logger.Info("System info", "cpu_cores", runtime.NumCPU(), "go_version", runtime.Version(), "os", runtime.GOOS, "arch", runtime.GOARCH)

	logger.Info("==============================")
}
