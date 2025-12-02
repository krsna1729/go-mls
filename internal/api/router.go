// Package api provides HTTP API handlers and routing
package api

import (
	"net/http"

	"go-mls/internal/app"
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
	// Relay routes (now Stream routes) - using canonical Api* handlers
	mux.HandleFunc("/api/relay/start", stream.ApiStartRelay(rt.stream))
	mux.HandleFunc("/api/relay/stop", stream.ApiStopRelay(rt.stream))
	mux.HandleFunc("/api/relay/status", stream.ApiRelayStatus(rt.stream))
	mux.HandleFunc("/api/relay/export", stream.ApiExportRelays(rt.stream))
	mux.HandleFunc("/api/relay/import", stream.ApiImportRelays(rt.stream))
	mux.HandleFunc("/api/relay/presets", stream.ApiRelayPresets())
	mux.HandleFunc("/api/relay/delete-input", stream.ApiDeleteInput(rt.stream))
	mux.HandleFunc("/api/relay/delete-output", stream.ApiDeleteOutput(rt.stream))

	// RTSP routes
	mux.HandleFunc("/api/rtsp/status", stream.ApiRTSPStatus(rt.rtsp))

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
