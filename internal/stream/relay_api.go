package stream

import (
	"go-mls/internal/httputil"
	"io"
	"net/http"
	"os"
)

func ApiStartRelay(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiStartRelay called")
		var req struct {
			InputURL       string            `json:"input_url"`
			OutputURL      string            `json:"output_url"`
			InputName      string            `json:"input_name"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			streamMgr.Logger.Error("apiStartRelay: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		// Validate required fields
		if req.InputName == "" || req.OutputName == "" {
			streamMgr.Logger.Error("apiStartRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		streamMgr.Logger.Debug("apiStartRelay: starting relay", "inputURL", req.InputURL, "outputURL", req.OutputURL, "inputName", req.InputName, "outputName", req.OutputName, "preset", req.PlatformPreset)

		// Apply preset and options using centralized helper
		opts, platformPreset := streamMgr.applyPresetAndOptions(req.PlatformPreset, req.FFmpegOptions, req.InputURL, req.OutputURL)

		if err := streamMgr.StartStream(req.InputURL, req.OutputURL, req.InputName, req.OutputName, opts, platformPreset); err != nil {
			streamMgr.Logger.Error("apiStartRelay: failed to start relay", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
		streamMgr.Logger.Debug("apiStartRelay: relay started successfully")
	}
}

func ApiStopRelay(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiStopRelay called")
		var req struct {
			InputURL   string `json:"input_url"`
			OutputURL  string `json:"output_url"`
			InputName  string `json:"input_name"`
			OutputName string `json:"output_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			streamMgr.Logger.Error("apiStopRelay: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			streamMgr.Logger.Error("apiStopRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		streamMgr.Logger.Debug("apiStopRelay: stopping relay", "inputURL", req.InputURL, "outputURL", req.OutputURL, "inputName", req.InputName, "outputName", req.OutputName)
		if err := streamMgr.StopStream(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
			streamMgr.Logger.Error("apiStopRelay: failed to stop relay", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
		streamMgr.Logger.Debug("apiStopRelay: relay stopped successfully")
	}
}

func ApiRelayStatus(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiRelayStatus called")
		httputil.WriteJSON(w, http.StatusOK, streamMgr.Status())
		streamMgr.Logger.Debug("apiRelayStatus: status returned")
	}
}

func ApiExportRelays(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiExportRelays called")
		if err := streamMgr.ExportConfig("relay_config.json"); err != nil {
			streamMgr.Logger.Error("apiExportRelays: failed to export config", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
		data, _ := os.ReadFile("relay_config.json")
		w.Write(data)
		streamMgr.Logger.Debug("apiExportRelays: config exported successfully")
	}
}

func ApiImportRelays(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiImportRelays called")
		file, _, err := r.FormFile("file")
		if err != nil {
			streamMgr.Logger.Error("apiImportRelays: no file uploaded", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "No file uploaded")
			return
		}
		defer file.Close()
		f, err := os.Create("relay_config.json")
		if err != nil {
			streamMgr.Logger.Error("apiImportRelays: failed to save file", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save file")
			return
		}
		defer f.Close()
		io.Copy(f, file)
		if err := streamMgr.ImportConfig("relay_config.json"); err != nil {
			streamMgr.Logger.Error("apiImportRelays: failed to import config", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "imported"})
		streamMgr.Logger.Debug("apiImportRelays: config imported successfully")
	}
}

func ApiRTSPStatus(rtspServer *RTSPServerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rtspServer == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "RTSP server not available")
			return
		}
		stats := rtspServer.GetStreamStats()
		httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"streams": stats,
			"total":   len(stats),
		})
	}
}

func ApiRelayPresets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presets := make(map[string]map[string]string)
		for name, preset := range PlatformPresets {
			presets[name] = map[string]string{
				"video_codec": preset.Options.VideoCodec,
				"audio_codec": preset.Options.AudioCodec,
				"resolution":  preset.Options.Resolution,
				"framerate":   preset.Options.Framerate,
				"bitrate":     preset.Options.Bitrate,
				"rotation":    preset.Options.Rotation,
			}
		}
		httputil.WriteJSON(w, http.StatusOK, presets)
	}
}

func ApiDeleteInput(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiDeleteInput called")
		var req struct {
			InputURL  string `json:"input_url"`
			InputName string `json:"input_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			streamMgr.Logger.Error("apiDeleteInput: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" {
			streamMgr.Logger.Error("apiDeleteInput: missing input name")
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}
		streamMgr.Logger.Debug("apiDeleteInput: deleting input", "inputURL", req.InputURL, "inputName", req.InputName)
		if err := streamMgr.DeleteInput(req.InputURL, req.InputName); err != nil {
			streamMgr.Logger.Error("apiDeleteInput: failed to delete input", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		streamMgr.Logger.Debug("apiDeleteInput: input deleted successfully")
	}
}

func ApiDeleteOutput(streamMgr *StreamManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamMgr.Logger.Debug("apiDeleteOutput called")
		var req struct {
			InputURL   string `json:"input_url"`
			OutputURL  string `json:"output_url"`
			InputName  string `json:"input_name"`
			OutputName string `json:"output_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			streamMgr.Logger.Error("apiDeleteOutput: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			streamMgr.Logger.Error("apiDeleteOutput: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		streamMgr.Logger.Debug("apiDeleteOutput: deleting output", "inputURL", req.InputURL, "outputURL", req.OutputURL, "inputName", req.InputName, "outputName", req.OutputName)
		if err := streamMgr.DeleteOutput(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
			streamMgr.Logger.Error("apiDeleteOutput: failed to delete output", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		streamMgr.Logger.Debug("apiDeleteOutput: output deleted successfully")
	}
}
