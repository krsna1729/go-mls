package app

import (
	"fmt"
	"time"

	"go-mls/internal/config"
	"go-mls/internal/ffmpeg"
	"go-mls/internal/hub"
	"go-mls/internal/ingest"
	"go-mls/internal/logger"
	"go-mls/internal/state"
	"go-mls/internal/worker"
)

type Context struct {
	Logger *logger.Logger
	Config *config.Config
	Store  *state.Store
	Hub    hub.Hub
	Ingest *ingest.Router
	HLSMgr *worker.HLSManager
}

func NewContext(cfg *config.Config, log *logger.Logger) (*Context, error) {
	ctx := &Context{
		Logger: log,
		Config: cfg,
		Store:  state.NewStore(),
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	hubType := hub.HubTypeRTMP
	if cfg.Relay.HubType != "" {
		hubType = hub.HubType(cfg.Relay.HubType)
	}

	internalRTMPHub := hub.NewRTMPHub(log, cfg.Relay.RTMPHub.Host, cfg.Relay.RTMPHub.Port)
	rtspHub := hub.NewRTSPHub(log, cfg.Relay.RTSPHub.Host, cfg.Relay.RTSPHub.Port)
	srtHub := hub.NewSRTHub(log, cfg.Relay.SRTHub.Host, cfg.Relay.SRTHub.Port)
	primaryHub := hub.Hub(internalRTMPHub)
	if hubType == hub.HubTypeRTSP {
		primaryHub = rtspHub
	} else if hubType == hub.HubTypeSRT {
		primaryHub = srtHub
	}
	ctx.Hub = hub.NewCompositeHub(primaryHub, internalRTMPHub, rtspHub, srtHub)
	internalRTMPPort := cfg.Relay.RTMPHub.Port

	ffmpeg.SetBinaryPath(cfg.FFmpeg.Path)
	ffmpeg.SetLogLevel(cfg.FFmpeg.LogLevel)

	ffmpegTimeout := time.Duration(cfg.Relay.OutputTimeout)
	_ = ffmpegTimeout

	ctx.Ingest = ingest.NewRouter(ctx.Store, log, ingest.Config{
		RTMPPort: internalRTMPPort,
		RTSPPort: cfg.Relay.RTSPHub.Port,
		SRTHost:  cfg.Relay.SRTHub.Host,
		SRTPort:  cfg.Relay.SRTHub.Port,
	})

	ctx.Hub.SetOnPublish(ctx.Ingest.OnPublish)
	ctx.Hub.SetOnUnpublish(ctx.Ingest.OnPublishEnd)
	ctx.Ingest.SetStreamEvictor(ctx.Hub.EvictStream)

	hlsPreset := cfg.HLS.FFmpegPreset
	if hlsPreset == "" {
		hlsPreset = "ultrafast"
	}

	ctx.HLSMgr = worker.NewHLSManager(
		ctx.Store,
		log,
		cfg.HLS.PlaylistBaseDir,
		hlsPreset,
		internalRTMPPort,
		time.Duration(cfg.HLS.ViewerHeartbeatTimeout),
		time.Duration(cfg.HLS.IdleTimeout),
	)

	return ctx, nil
}

func (c *Context) Start() error {
	if err := c.Hub.Start(); err != nil {
		return fmt.Errorf("failed to start ingest hubs: %w", err)
	}

	c.Logger.Info("Application context initialized successfully")
	return nil
}

func (c *Context) Shutdown() {
	c.Logger.Info("Shutting down application context...")

	c.HLSMgr.Shutdown()
	c.Ingest.Shutdown()
	c.Hub.Stop()

	c.Logger.Info("Application context shutdown complete")
}
