package app

import (
	"fmt"
	"time"

	"go-mls/internal/config"
	"go-mls/internal/logger"
	"go-mls/internal/stream"
)

type Context struct {
	Logger   *logger.Logger
	Config   *config.Config
	Pipeline *stream.Pipeline
	RTSP     *stream.RTSPServerManager
}

func NewContext(cfg *config.Config, log *logger.Logger) (*Context, error) {
	ctx := &Context{
		Logger: log,
		Config: cfg,
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	ctx.RTSP = stream.NewRTSPServerManager(log, cfg.Relay.RTSPServer.Host, cfg.Relay.RTSPServer.Port)

	ffmpegTimeout := time.Duration(cfg.Relay.OutputTimeout)
	ctx.Pipeline = stream.NewPipeline(log, cfg.Recording.Directory, ffmpegTimeout)
	ctx.Pipeline.SetRTSPServer(ctx.RTSP)

	return ctx, nil
}

func (c *Context) Start() error {
	if err := c.RTSP.Start(); err != nil {
		return fmt.Errorf("failed to start RTSP server: %w", err)
	}

	c.Logger.Info("Application context initialized successfully")
	return nil
}

func (c *Context) Shutdown() {
	c.Logger.Info("Shutting down application context...")

	c.Pipeline.Shutdown()

	c.RTSP.Stop()

	c.Logger.Info("Application context shutdown complete")
}
