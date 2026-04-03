package stream

import (
	"context"
	"go-mls/internal/httputil"
	"net/http"
	"strings"
	"time"
)

func ApiWatchInputHLS(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/relay/watch-input/hls/"), "/", 2)
		if len(parts) != 2 {
			p.Logger.Error("Invalid HLS request path", "path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		inputName, file := parts[0], parts[1]
		if inputName == "" || file == "" {
			p.Logger.Error("Missing inputName or file in HLS request", "path", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		p.Logger.Debug("HLS serve request", "inputName", inputName, "file", file)

		p.mu.RLock()
		var sess *PipelineHLSSession
		var dir string
		var ready bool

		relay, exists := p.relays[inputName]
		if exists && relay.HLSSession != nil {
			sess = relay.HLSSession
			sess.Mu.RLock()
			ready = sess.Ready
			dir = sess.Dir
			sess.Mu.RUnlock()
		}
		p.mu.RUnlock()

		if sess == nil {
			http.Error(w, "HLS session not found", http.StatusNotFound)
			return
		}

		if !ready {
			http.Error(w, "HLS session not ready", http.StatusServiceUnavailable)
			return
		}

		if strings.HasSuffix(file, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasSuffix(file, ".ts") {
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}

		http.ServeFile(w, r, dir+"/"+file)
	}
}

func ApiStartHLSViewer(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
			Preset    string `json:"preset"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			p.Logger.Error("HLS start viewer: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}

		p.mu.RLock()
		relay, exists := p.relays[req.InputName]
		hasInput := exists && relay.Input != nil
		p.mu.RUnlock()

		if !hasInput {
			httputil.WriteError(w, http.StatusNotFound, "Input not found")
			return
		}

		p.mu.Lock()
		relay, exists = p.relays[req.InputName]
		if !exists || relay.HLSSession == nil {
			preset := req.Preset
			if preset == "" {
				preset = "ultrafast"
			}
			if err := p.StartHLS(context.Background(), req.InputName, req.InputName, preset); err != nil {
				p.Logger.Error("Failed to start HLS session", "err", err)
				p.mu.Unlock()
				httputil.WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			relay = p.relays[req.InputName]
		}

		viewerID := "viewer-" + string(rune(time.Now().UnixNano()))
		if relay.HLSSession != nil {
			relay.HLSSession.Mu.Lock()
			relay.HLSSession.ViewerIDs[viewerID] = time.Now()
			relay.HLSSession.LastAccess = time.Now()
			relay.HLSSession.Mu.Unlock()
		}
		p.mu.Unlock()

		p.Logger.Info("HLS viewer started", "inputName", req.InputName, "viewerID", viewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{
			"viewer_id":    viewerID,
			"playlist_url": "/api/relay/watch-input/hls/" + req.InputName + "/index.m3u8",
		})
	}
}

func ApiStopHLSViewer(p *Pipeline) http.HandlerFunc {
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
			httputil.WriteError(w, http.StatusBadRequest, "Input name and viewer ID are required")
			return
		}

		p.mu.Lock()
		relay, exists := p.relays[req.InputName]
		if exists && relay.HLSSession != nil {
			relay.HLSSession.Mu.Lock()
			delete(relay.HLSSession.ViewerIDs, req.ViewerID)
			relay.HLSSession.Mu.Unlock()
		}
		p.mu.Unlock()

		p.Logger.Info("HLS viewer stopped", "inputName", req.InputName, "viewerID", req.ViewerID)
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

func ApiHLSViewerHeartbeat(p *Pipeline) http.HandlerFunc {
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
			httputil.WriteError(w, http.StatusBadRequest, "Input name and viewer ID are required")
			return
		}

		p.mu.Lock()
		relay, exists := p.relays[req.InputName]
		if exists && relay.HLSSession != nil {
			relay.HLSSession.Mu.Lock()
			if _, ok := relay.HLSSession.ViewerIDs[req.ViewerID]; ok {
				relay.HLSSession.ViewerIDs[req.ViewerID] = time.Now()
				relay.HLSSession.LastAccess = time.Now()
			}
			relay.HLSSession.Mu.Unlock()
		}
		p.mu.Unlock()

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
