package api

import (
	"net/http"

	"go-mls/internal/app"
	"go-mls/internal/stream"
)

type Router struct {
	Pipeline *stream.Pipeline
	RTSP     *stream.RTSPServerManager
}

func NewRouter(appCtx *app.Context) *Router {
	return &Router{
		Pipeline: appCtx.Pipeline,
		RTSP:     appCtx.RTSP,
	}
}

func (rt *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/relay/start", stream.ApiStartOutputRelay(rt.Pipeline))
	mux.HandleFunc("/api/relay/stop", stream.ApiStopOutputRelay(rt.Pipeline))
	mux.HandleFunc("/api/relay/status", stream.ApiRelayStatus(rt.Pipeline))
	mux.HandleFunc("/api/relay/export", stream.ApiExportRelays(rt.Pipeline))
	mux.HandleFunc("/api/relay/import", stream.ApiImportRelays(rt.Pipeline))
	mux.HandleFunc("/api/relay/presets", stream.ApiRelayPresets())
	mux.HandleFunc("/api/relay/delete-input", stream.ApiDeleteInput(rt.Pipeline))
	mux.HandleFunc("/api/relay/delete-output", stream.ApiDeleteOutput(rt.Pipeline))

	mux.HandleFunc("/api/rtsp/status", stream.ApiRTSPStatus(rt.RTSP))

	mux.HandleFunc("/api/recording/start", stream.ApiStartRecording(rt.Pipeline))
	mux.HandleFunc("/api/recording/stop", stream.ApiStopRecording(rt.Pipeline))
	mux.HandleFunc("/api/recording/list", stream.ApiListRecordings(rt.Pipeline))
	mux.HandleFunc("/api/recording/delete", stream.ApiDeleteRecording(rt.Pipeline))
	mux.HandleFunc("/api/recording/download", stream.ApiDownloadRecording(rt.Pipeline))
	mux.HandleFunc("/api/recording/sse", stream.ApiRecordingsSSE(rt.Pipeline))

	mux.HandleFunc("/api/relay/watch-input/hls/", stream.ApiWatchInputHLS(rt.Pipeline))
	mux.HandleFunc("/api/relay/hls/start-viewer", stream.ApiStartHLSViewer(rt.Pipeline))
	mux.HandleFunc("/api/relay/hls/stop-viewer", stream.ApiStopHLSViewer(rt.Pipeline))
	mux.HandleFunc("/api/relay/hls/heartbeat", stream.ApiHLSViewerHeartbeat(rt.Pipeline))
}
