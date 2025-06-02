package main

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"

	"go-mls/internal/logger"
	"go-mls/internal/stream"
)

//go:embed web/*
var webAssets embed.FS

func apiStartRelay(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiStartRelay called")
		var req struct {
			InputURL  string `json:"input_url"`
			OutputURL string `json:"output_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			relayMgr.Logger.Error("apiStartRelay: failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
			return
		}
		relayMgr.Logger.Debug("apiStartRelay: starting relay for input=%s, output=%s", req.InputURL, req.OutputURL)
		if err := relayMgr.StartRelay(req.InputURL, req.OutputURL); err != nil {
			relayMgr.Logger.Error("apiStartRelay: failed to start relay: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "started"})
		relayMgr.Logger.Debug("apiStartRelay: relay started successfully")
	}
}

func apiStopRelay(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiStopRelay called")
		var req struct {
			InputURL  string `json:"input_url"`
			OutputURL string `json:"output_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			relayMgr.Logger.Error("apiStopRelay: failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
			return
		}
		relayMgr.Logger.Debug("apiStopRelay: stopping relay for input=%s, output=%s", req.InputURL, req.OutputURL)
		if err := relayMgr.StopRelay(req.InputURL, req.OutputURL); err != nil {
			relayMgr.Logger.Error("apiStopRelay: failed to stop relay: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
		relayMgr.Logger.Debug("apiStopRelay: relay stopped successfully")
	}
}

func apiRelayStatus(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiRelayStatus called")
		json.NewEncoder(w).Encode(relayMgr.Status())
		relayMgr.Logger.Debug("apiRelayStatus: status returned")
	}
}

func apiExportRelays(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiExportRelays called")
		if err := relayMgr.ExportConfig("relay_config.json"); err != nil {
			relayMgr.Logger.Error("apiExportRelays: failed to export config: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
		data, _ := os.ReadFile("relay_config.json")
		w.Write(data)
		relayMgr.Logger.Debug("apiExportRelays: config exported successfully")
	}
}

func apiImportRelays(relayMgr *stream.RelayManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayMgr.Logger.Debug("apiImportRelays called")
		file, _, err := r.FormFile("file")
		if err != nil {
			relayMgr.Logger.Error("apiImportRelays: no file uploaded: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "No file uploaded"})
			return
		}
		defer file.Close()
		f, err := os.Create("relay_config.json")
		if err != nil {
			relayMgr.Logger.Error("apiImportRelays: failed to save file: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
			return
		}
		defer f.Close()
		io.Copy(f, file)
		if err := relayMgr.ImportConfig("relay_config.json"); err != nil {
			relayMgr.Logger.Error("apiImportRelays: failed to import config: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "imported"})
		relayMgr.Logger.Debug("apiImportRelays: config imported successfully")
	}
}

func main() {
	logger := logger.NewLogger()
	logger.Debug("main: initializing relay manager")
	relayMgr := stream.NewRelayManager(logger)

	// Use embedded static assets
	staticFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Error("Failed to create sub FS for web assets: %v", err)
		os.Exit(1)
	}
	fs := http.FileServer(http.FS(staticFS))
	http.Handle("/", fs)

	http.HandleFunc("/api/relay/start", apiStartRelay(relayMgr))
	http.HandleFunc("/api/relay/stop", apiStopRelay(relayMgr))
	http.HandleFunc("/api/relay/status", apiRelayStatus(relayMgr))
	http.HandleFunc("/api/relay/export", apiExportRelays(relayMgr))
	http.HandleFunc("/api/relay/import", apiImportRelays(relayMgr))

	logger.Info("Go-MLS relay manager running at http://localhost:8080 ...")
	logger.Debug("main: server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Error("Server error: %v", err)
	}
	logger.Debug("main: server shutdown")
}
