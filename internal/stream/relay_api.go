package stream

import (
	"context"
	"go-mls/internal/httputil"
	"io"
	"net/http"
	"os"
)

func ApiStartOutputRelay(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputURL       string            `json:"input_url"`
			OutputURL      string            `json:"output_url"`
			InputName      string            `json:"input_name"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			p.Logger.Error("apiStartOutputRelay: failed to decode request", "err", err)
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" || req.OutputName == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Input and output names are required")
			return
		}

		ctx := context.Background()

		if _, exists := p.GetInputURL(req.InputName); !exists {
			if err := p.StartInput(ctx, req.InputName, req.InputURL); err != nil {
				p.Logger.Error("apiStartOutputRelay: failed to start input", "err", err)
				httputil.WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		opts := ApplyPresetAndOptions(req.PlatformPreset, req.FFmpegOptions)

		if err := p.StartOutput(ctx, req.OutputName, req.InputName, req.OutputURL, opts, req.PlatformPreset); err != nil {
			p.Logger.Error("apiStartOutputRelay: failed to start output", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
	}
}

func ApiStopOutputRelay(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OutputName string `json:"output_name"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.OutputName == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Output name is required")
			return
		}

		if err := p.StopOutput(req.OutputName); err != nil {
			p.Logger.Error("apiStopOutputRelay: failed to stop output", "err", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

func ApiRelayStatus(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, http.StatusOK, p.Status())
	}
}

func ApiExportRelays(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := p.ExportConfig("relay_config.json"); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
		data, _ := os.ReadFile("relay_config.json")
		w.Write(data)
	}
}

func ApiImportRelays(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("file")
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "No file uploaded")
			return
		}
		defer file.Close()
		f, err := os.Create("relay_config.json")
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save file")
			return
		}
		defer f.Close()
		io.Copy(f, file)
		if err := p.ImportConfig("relay_config.json"); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "imported"})
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
		type presetOptions struct {
			VideoCodec string `json:"video_codec"`
			AudioCodec string `json:"audio_codec"`
			Resolution string `json:"resolution"`
			Framerate  string `json:"framerate"`
			Bitrate    string `json:"bitrate"`
			Rotation   string `json:"rotation"`
		}
		type presetInfo struct {
			Name    string        `json:"name"`
			Options presetOptions `json:"options"`
		}
		presets := make(map[string]presetInfo)
		presets["YouTube"] = presetInfo{
			Name: "YouTube",
			Options: presetOptions{
				VideoCodec: "libx264",
				AudioCodec: "aac",
				Resolution: "1920x1080",
				Framerate:  "30",
				Bitrate:    "4500k",
			},
		}
		presets["Facebook"] = presetInfo{
			Name: "Facebook",
			Options: presetOptions{
				VideoCodec: "libx264",
				AudioCodec: "aac",
				Resolution: "1280x720",
				Framerate:  "30",
				Bitrate:    "2500k",
			},
		}
		presets["Twitch"] = presetInfo{
			Name: "Twitch",
			Options: presetOptions{
				VideoCodec: "libx264",
				AudioCodec: "aac",
				Resolution: "1920x1080",
				Framerate:  "60",
				Bitrate:    "6000k",
			},
		}
		httputil.WriteJSON(w, http.StatusOK, presets)
	}
}

func ApiDeleteInput(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InputName string `json:"input_name"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.InputName == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}

		if err := p.DeleteInput(req.InputName); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func ApiDeleteOutput(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OutputName string `json:"output_name"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.OutputName == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Output name is required")
			return
		}

		if err := p.StopOutput(req.OutputName); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
