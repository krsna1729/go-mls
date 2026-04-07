package app

import (
	"fmt"
	"time"

	"go-mls/internal/config"
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

	var hubPort int
	var hubHost string
	if hubType == hub.HubTypeRTSP {
		hubHost = cfg.Relay.RTSPHub.Host
		hubPort = cfg.Relay.RTSPHub.Port
	} else {
		hubHost = cfg.Relay.RTMPHub.Host
		hubPort = cfg.Relay.RTMPHub.Port
	}

	ctx.Hub = hub.NewHub(log, hubType, hubHost, hubPort)

	ffmpegTimeout := time.Duration(cfg.Relay.OutputTimeout)
	_ = ffmpegTimeout

	ctx.Ingest = ingest.NewRouter(ctx.Store, log, ingest.Config{
		FFMpegPath: cfg.FFmpeg.Path,
		RTMPPort:   hubPort,
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
		hubPort,
		time.Duration(cfg.HLS.ViewerHeartbeatTimeout),
		time.Duration(cfg.HLS.IdleTimeout),
	)

	return ctx, nil
}

func (c *Context) Start() error {
	if err := c.Hub.Start(); err != nil {
		return fmt.Errorf("failed to start RTMP hub: %w", err)
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
