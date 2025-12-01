// Package app provides application-level context and dependency management
package app

import (
	"fmt"
	"time"

	"go-mls/internal/config"
	"go-mls/internal/logger"
	"go-mls/internal/stream"
)

// Context holds all application dependencies and provides centralized initialization
type Context struct {
	Logger    *logger.Logger
	Config    *config.Config
	Relay     *stream.RelayManager
	Recording *stream.RecordingManager
	HLS       *stream.HLSManager
	RTSP      *stream.RTSPServerManager
}

// NewContext creates and initializes a new application context with all dependencies
// It handles proper initialization order and cross-wiring of components
func NewContext(cfg *config.Config, log *logger.Logger) (*Context, error) {
	ctx := &Context{
		Logger: log,
		Config: cfg,
	}

	// Validate configuration before initializing components
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize RTSP server with configuration
	ctx.RTSP = stream.NewRTSPServerManager(log, cfg.Relay.RTSPServer.Host, cfg.Relay.RTSPServer.Port)

	// Initialize relay manager
	ctx.Relay = stream.NewRelayManager(log, cfg.Recording.Directory, cfg.FFmpeg.LogLevel)
	ctx.Relay.SetRTSPServer(ctx.RTSP)
	ctx.Relay.SetTimeouts(
		time.Duration(cfg.Relay.InputTimeout),
		time.Duration(cfg.Relay.OutputTimeout),
	)

	// Initialize recording manager
	// Pass Logger, recordingDir, and InputRelays as StreamProvider
	ctx.Recording = stream.NewRecordingManager(log, cfg.Recording.Directory, ctx.Relay.InputRelays)

	// Initialize HLS manager
	// Pass Logger, hlsDir, and InputRelays as StreamProvider
	ctx.HLS = stream.NewHLSManager(log, cfg.HLS.PlaylistBaseDir, ctx.Relay.InputRelays)

	// Wire up cross-references
	// RelayManager needs HLS and Recording managers for legacy reasons (if any) or cleanup
	ctx.Relay.SetHLSManager(ctx.HLS)
	ctx.Relay.SetRecordingManager(ctx.Recording)
	// HLS and Recording managers now use StreamProvider (ctx.Relay.InputRelays) directly

	return ctx, nil
}

// Start starts all components in the proper order
func (c *Context) Start() error {
	// Start RTSP server
	if err := c.RTSP.Start(); err != nil {
		return fmt.Errorf("failed to start RTSP server: %w", err)
	}

	c.Logger.Info("Application context initialized successfully")
	return nil
}

// Shutdown gracefully shuts down all components in reverse order
func (c *Context) Shutdown() {
	c.Logger.Info("Shutting down application context...")

	// Shutdown HLS manager
	c.Logger.Info("Shutting down HLS manager...")
	c.HLS.Shutdown()

	// Give clients time to fetch final dummy playlist
	time.Sleep(15 * time.Second)

	// Stop all recordings
	c.Logger.Info("Shutting down recording manager...")
	c.Recording.Shutdown()

	// Stop all active relays
	c.Logger.Info("Stopping all active relays...")
	c.Relay.StopAllRelays()

	// Stop RTSP server
	c.Logger.Info("Stopping RTSP server...")
	c.RTSP.Stop()

	// Give time for cleanup
	time.Sleep(3 * time.Second)

	c.Logger.Info("Application context shutdown complete")
}
