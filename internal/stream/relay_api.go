package stream

import (
	"go-mls/internal/httputil"
	"io"
	"net/http"
	"os"
)

func ApiStartRelay(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiStartRelay called")
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
			relayMgr.Logger.Error("apiStartRelay: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		// Validate required fields
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiStartRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}

		relayMgr.Logger.Debug("apiStartRelay: starting relay for input=%s, output=%s, input_name=%s, output_name=%s, preset=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName, req.PlatformPreset)

		// Check if preset/options are provided in request, otherwise try to get from stored config
		platformPreset := req.PlatformPreset
		var opts *FFmpegOptions
		if req.FFmpegOptions != nil {
			opts = &FFmpegOptions{
				VideoCodec: req.FFmpegOptions["video_codec"],
				AudioCodec: req.FFmpegOptions["audio_codec"],
				Resolution: req.FFmpegOptions["resolution"],
				Framerate:  req.FFmpegOptions["framerate"],
				Bitrate:    req.FFmpegOptions["bitrate"],
				Rotation:   req.FFmpegOptions["rotation"],
			}
		} else if platformPreset == "" {
			// Try to get stored configuration for this endpoint
			storedPreset, storedOpts, err := relayMgr.GetEndpointConfig(req.InputURL, req.OutputURL)
			if err == nil {
				platformPreset = storedPreset
				opts = storedOpts
				relayMgr.Logger.Debug("apiStartRelay: using stored config - preset=%s, options=%+v", platformPreset, opts)
			}
		}
		if err := relayMgr.StartRelayWithOptions(req.InputURL, req.OutputURL, req.InputName, req.OutputName, opts, platformPreset); err != nil {
			relayMgr.Logger.Error("apiStartRelay: failed to start relay: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
		relayMgr.Logger.Debug("apiStartRelay: relay started successfully")
	}
}

func ApiStopRelay(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiStopRelay called")
		var req struct {
			InputURL   string `json:"input_url"`
			OutputURL  string `json:"output_url"`
			InputName  string `json:"input_name"`
			OutputName string `json:"output_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("apiStopRelay: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiStopRelay: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		relayMgr.Logger.Debug("apiStopRelay: stopping relay for input=%s, output=%s, input_name=%s, output_name=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName)
		if err := relayMgr.StopRelay(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
			relayMgr.Logger.Error("apiStopRelay: failed to stop relay: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
		relayMgr.Logger.Debug("apiStopRelay: relay stopped successfully")
	}
}

func ApiRelayStatus(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiRelayStatus called")
		httputil.WriteJSON(w, http.StatusOK, relayMgr.StatusV2())
		relayMgr.Logger.Debug("apiRelayStatus: status returned")
	}
}

func ApiExportRelays(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiExportRelays called")
		if err := relayMgr.ExportConfig("relay_config.json"); err != nil {
			relayMgr.Logger.Error("apiExportRelays: failed to export config: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
		data, _ := os.ReadFile("relay_config.json")
		w.Write(data)
		relayMgr.Logger.Debug("apiExportRelays: config exported successfully")
	}
}

func ApiImportRelays(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiImportRelays called")
		file, _, err := r.FormFile("file")
		if err != nil {
			relayMgr.Logger.Error("apiImportRelays: no file uploaded: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "No file uploaded")
			return
		}
		defer file.Close()
		f, err := os.Create("relay_config.json")
		if err != nil {
			relayMgr.Logger.Error("apiImportRelays: failed to save file: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save file")
			return
		}
		defer f.Close()
		io.Copy(f, file)
		if err := relayMgr.ImportConfig("relay_config.json"); err != nil {
			relayMgr.Logger.Error("apiImportRelays: failed to import config: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "imported"})
		relayMgr.Logger.Debug("apiImportRelays: config imported successfully")
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

func ApiDeleteInput(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiDeleteInput called")
		var req struct {
			InputURL  string `json:"input_url"`
			InputName string `json:"input_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("apiDeleteInput: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" {
			relayMgr.Logger.Error("apiDeleteInput: missing input name")
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}
		relayMgr.Logger.Debug("apiDeleteInput: deleting input for input=%s, input_name=%s", req.InputURL, req.InputName)
		if err := relayMgr.DeleteInput(req.InputURL, req.InputName); err != nil {
			relayMgr.Logger.Error("apiDeleteInput: failed to delete input: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		relayMgr.Logger.Debug("apiDeleteInput: input deleted successfully")
	}
}

func ApiDeleteOutput(relayMgr *RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiDeleteOutput called")
		var req struct {
			InputURL   string `json:"input_url"`
			OutputURL  string `json:"output_url"`
			InputName  string `json:"input_name"`
			OutputName string `json:"output_name"`
		}

		// Use secure JSON decoding with size limits
		if err := httputil.DecodeJSON(r, &req); err != nil {
			relayMgr.Logger.Error("apiDeleteOutput: failed to decode request: %v", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.InputName == "" || req.OutputName == "" {
			relayMgr.Logger.Error("apiDeleteOutput: missing input or output name")
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}
		relayMgr.Logger.Debug("apiDeleteOutput: deleting output for input=%s, output=%s, input_name=%s, output_name=%s", req.InputURL, req.OutputURL, req.InputName, req.OutputName)
		if err := relayMgr.DeleteOutput(req.InputURL, req.OutputURL, req.InputName, req.OutputName); err != nil {
			relayMgr.Logger.Error("apiDeleteOutput: failed to delete output: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		relayMgr.Logger.Debug("apiDeleteOutput: output deleted successfully")
	}
}
