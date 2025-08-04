package stream

import (
	"go-mls/internal/httputil"
	"net/http"
	"strings"
)

// apiWatchInputHLS handles HLS playlist/segment requests for a given input relay.
func ApiWatchInputHLS(hlsMgr *HLSManager, relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// URL: /api/relay/watch-input/hls/{inputName}/{file}
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/relay/watch-input/hls/"), "/", 2)
		if len(parts) != 2 {
			relayMgr.Logger.Error("Invalid HLS request path", "path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		inputName, file := parts[0], parts[1]
		if inputName == "" || file == "" {
			relayMgr.Logger.Error("Missing inputName or file in HLS request", "path", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// HLS manager will handle starting input relay if needed
		hlsMgr.ServeHLS(w, r, inputName, file, "")
	}
}

// apiStartHLSViewer creates a new HLS viewer session
func ApiStartHLSViewer(hlsMgr *HLSManager, relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("HLS start viewer: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" {
			relayMgr.Logger.Error("HLS start viewer: missing input name")
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}

		// HLS manager will handle starting input relay if needed
		viewerID, err := hlsMgr.AddViewer(req.InputName)
		if err != nil {
			relayMgr.Logger.Error("HLS start viewer: failed to add viewer", "inputName", req.InputName, "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to start HLS viewer")
			return
		}

		relayMgr.Logger.Info("HLS viewer started", "inputName", req.InputName, "viewerID", viewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{
			"viewer_id":    viewerID,
			"playlist_url": "/api/relay/watch-input/hls/" + req.InputName + "/index.m3u8",
		})
	}
}

// apiStopHLSViewer stops an HLS viewer session
func ApiStopHLSViewer(hlsMgr *HLSManager, relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
			ViewerID  string `json:"viewer_id"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("HLS stop viewer: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" || req.ViewerID == "" {
			relayMgr.Logger.Error("HLS stop viewer: missing input name or viewer ID")
			httputil.WriteError(w, http.StatusBadRequest, "Input name and viewer ID are required")
			return
		}

		hlsMgr.RemoveViewer(req.InputName, req.ViewerID)
		relayMgr.Logger.Info("HLS viewer stopped", "inputName", req.InputName, "viewerID", req.ViewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// apiHLSViewerHeartbeat updates viewer heartbeat
func ApiHLSViewerHeartbeat(hlsMgr *HLSManager) http.HandlerFunc {
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
			hlsMgr.logger.WarnRateLimited("HLS heartbeat: missing input name or viewer ID", "inputName", req.InputName, "viewerID", req.ViewerID)
			httputil.WriteError(w, http.StatusBadRequest, "Input name and viewer ID are required")
			return
		}

		err := hlsMgr.UpdateViewerHeartbeat(req.InputName, req.ViewerID)
		if err != nil {
			if err.Error() == "session not found" {
				hlsMgr.logger.WarnRateLimited("HLS heartbeat: session not found", "inputName", req.InputName, "viewerID", req.ViewerID)
				httputil.WriteError(w, http.StatusGone, "Viewer session expired or input deleted")
				return
			}
			// Other errors (e.g., viewerID not found)
			hlsMgr.logger.WarnRateLimited("HLS heartbeat error", "err", err, "inputName", req.InputName, "viewerID", req.ViewerID)
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
