// Package api provides HTTP API handlers and routing
package api

import (
	"net/http"

	"go-mls/internal/app"
	"go-mls/internal/stream"
)

// Router handles all HTTP API routes
type Router struct {
	stream    *stream.StreamManager
	recording *stream.RecordingManager
	hls       *stream.HLSManager
	rtsp      *stream.RTSPServerManager
}

// NewRouter creates a new API router from an application context
func NewRouter(appCtx *app.Context) *Router {
	return &Router{
		stream:    appCtx.Stream,
		recording: appCtx.Recording,
		hls:       appCtx.HLS,
		rtsp:      appCtx.RTSP,
	}
}

// RegisterRoutes registers all API routes with the given mux
func (rt *Router) RegisterRoutes(mux *http.ServeMux) {
	// Health and operational endpoints (no versioning needed)
	mux.HandleFunc("/health", HealthHandler())
	mux.HandleFunc("/ready", ReadinessHandler(rt.rtsp))
	mux.HandleFunc("/version", VersionHandler())
	mux.Handle("/metrics", stream.MetricsHandler())

	// Helper to register both /api/v1/... and /api/... (backward compat)
	registerAPI := func(path string, handler http.HandlerFunc) {
		mux.Handle("/api/v1"+path, stream.MetricsMiddleware(handler))
		mux.Handle("/api"+path, stream.MetricsMiddleware(handler)) // backward compatibility
	}

	// Relay routes (now Stream routes) - using canonical Api* handlers
	registerAPI("/relay/start", stream.ApiStartOutputRelay(rt.stream))
	registerAPI("/relay/stop", stream.ApiStopOutputRelay(rt.stream))
	registerAPI("/relay/status", stream.ApiRelayStatus(rt.stream))
	registerAPI("/relay/export", stream.ApiExportRelays(rt.stream))
	registerAPI("/relay/import", stream.ApiImportRelays(rt.stream))
	registerAPI("/relay/presets", stream.ApiRelayPresets())
	registerAPI("/relay/delete-input", stream.ApiDeleteInput(rt.stream))
	registerAPI("/relay/delete-output", stream.ApiDeleteOutput(rt.stream))

	// RTSP routes
	registerAPI("/rtsp/status", stream.ApiRTSPStatus(rt.rtsp))

	// Recording routes
	registerAPI("/recording/start", stream.ApiStartRecording(rt.recording))
	registerAPI("/recording/stop", stream.ApiStopRecording(rt.recording))
	registerAPI("/recording/list", stream.ApiListRecordings(rt.recording))
	registerAPI("/recording/delete", stream.ApiDeleteRecording(rt.recording))
	registerAPI("/recording/download", stream.ApiDownloadRecording(rt.recording))
	registerAPI("/recording/sse", stream.ApiRecordingsSSE())

	// HLS routes (path prefix requires direct registration)
	// Note: ApiWatchInputHLS and others still need StreamManager for input relay coordination
	mux.Handle("/api/v1/relay/watch-input/hls/", stream.MetricsMiddleware(stream.ApiWatchInputHLS(rt.hls, rt.stream)))
	mux.Handle("/api/relay/watch-input/hls/", stream.MetricsMiddleware(stream.ApiWatchInputHLS(rt.hls, rt.stream)))
	registerAPI("/relay/hls/start-viewer", stream.ApiStartHLSViewer(rt.hls, rt.stream))
	registerAPI("/relay/hls/stop-viewer", stream.ApiStopHLSViewer(rt.hls, rt.stream))
	registerAPI("/relay/hls/heartbeat", stream.ApiHLSViewerHeartbeat(rt.hls))
}
