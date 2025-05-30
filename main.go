package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"go-mls/internal/config"
	"go-mls/internal/stream"
)

var (
	streamMgr *stream.StreamManager = stream.NewStreamManager()
	logr                            = config.NewLogger()
	cfgStore                        = config.NewConfigStore("relay_config.json")
)

type StreamStatus struct {
	Running   bool   `json:"running"`
	Message   string `json:"message"`
	InputURL  string `json:"input_url,omitempty"`
	OutputURL string `json:"output_url,omitempty"`
	LastCmd   string `json:"last_cmd,omitempty"`
}

func apiStartStream(w http.ResponseWriter, r *http.Request) {
	logr.Info("Received start stream request")
	var relayCfg stream.RTMPRelayConfig
	if err := json.NewDecoder(r.Body).Decode(&relayCfg); err != nil {
		logr.Error("Invalid relay config: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(StreamStatus{Running: false, Message: "Invalid config: " + err.Error()})
		return
	}
	if err := streamMgr.StartRelay(relayCfg); err != nil {
		logr.Error("Failed to start relay: %v", err)
		json.NewEncoder(w).Encode(StreamStatus{Running: false, Message: "Failed to start: " + err.Error()})
		return
	}
	logr.Info("Stream relay started: %s -> %s", relayCfg.InputURL, relayCfg.OutputURL)
	json.NewEncoder(w).Encode(StreamStatus{Running: true, Message: "Stream started", InputURL: relayCfg.InputURL, OutputURL: relayCfg.OutputURL})
}

func apiStopStream(w http.ResponseWriter, r *http.Request) {
	logr.Info("Received stop stream request")
	if err := streamMgr.Stop(); err != nil {
		logr.Error("Failed to stop stream: %v", err)
		json.NewEncoder(w).Encode(StreamStatus{Running: true, Message: "Failed to stop: " + err.Error()})
		return
	}
	logr.Info("Stream stopped successfully")
	json.NewEncoder(w).Encode(StreamStatus{Running: false, Message: "Stream stopped"})
}

func apiStatus(w http.ResponseWriter, r *http.Request) {
	logr.Debug("Status requested")
	running, relayCfg, lastCmd := streamMgr.Status()
	json.NewEncoder(w).Encode(StreamStatus{
		Running: running,
		Message: func() string {
			if running {
				return "Stream running"
			} else {
				return "Idle"
			}
		}(),
		InputURL:  relayCfg.InputURL,
		OutputURL: relayCfg.OutputURL,
		LastCmd:   lastCmd,
	})
}

func apiSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		stream.RTMPRelayConfig
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logr.Error("Invalid config for save: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid config: " + err.Error()})
		return
	}
	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Config name required"})
		return
	}
	if err := cfgStore.SaveNamed(req.Name, req.RTMPRelayConfig); err != nil {
		logr.Error("Failed to save config: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save: " + err.Error()})
		return
	}
	logr.Info("Relay config '%s' saved", req.Name)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func apiLoadConfig(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Config name required"})
		return
	}
	cfg, err := cfgStore.LoadNamed(name)
	if err != nil {
		logr.Error("Failed to load config: %v", err)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "No config found"})
		return
	}
	json.NewEncoder(w).Encode(cfg)
}

func apiListConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := cfgStore.LoadAll()
	if err != nil {
		logr.Error("Failed to list configs: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to list configs"})
		return
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	json.NewEncoder(w).Encode(names)
}

func apiCleanConfigs(w http.ResponseWriter, r *http.Request) {
	if err := cfgStore.Clean(); err != nil {
		logr.Error("Failed to clean configs: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to clean configs"})
		return
	}
	logr.Info("Relay configs cleaned")
	json.NewEncoder(w).Encode(map[string]string{"status": "cleaned"})
}

func apiDeleteConfig(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Config name required"})
		return
	}
	if err := cfgStore.DeleteNamed(name); err != nil {
		logr.Error("Failed to delete config: %v", err)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Config not found"})
		return
	}
	logr.Info("Relay config '%s' deleted", name)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func apiExportConfigs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
	file, err := os.Open(cfgStore.FilePath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "No config file found"})
		return
	}
	defer file.Close()
	io.Copy(w, file)
}

func apiImportConfigs(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "No file uploaded"})
		return
	}
	defer file.Close()
	configs := map[string]stream.RTMPRelayConfig{}
	if err := json.NewDecoder(file).Decode(&configs); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	f, err := os.Create(cfgStore.FilePath)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(configs); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to write file"})
		return
	}
	logr.Info("Relay configs imported")
	json.NewEncoder(w).Encode(map[string]string{"status": "imported"})
}

func apiRelayLogs(w http.ResponseWriter, r *http.Request) {
	logs := streamMgr.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": logs})
}

func main() {
	// Serve static files from web/static
	fs := http.FileServer(http.Dir("web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// API endpoints
	http.HandleFunc("/api/start", apiStartStream)
	http.HandleFunc("/api/stop", apiStopStream)
	http.HandleFunc("/api/status", apiStatus)
	http.HandleFunc("/api/save-config", apiSaveConfig)
	http.HandleFunc("/api/load-config", apiLoadConfig)
	http.HandleFunc("/api/list-configs", apiListConfigs)
	http.HandleFunc("/api/clean-configs", apiCleanConfigs)
	http.HandleFunc("/api/delete-config", apiDeleteConfig)
	http.HandleFunc("/api/export-configs", apiExportConfigs)
	http.HandleFunc("/api/import-configs", apiImportConfigs)
	http.HandleFunc("/api/relay/logs", apiRelayLogs)

	logr.Info("Go-MLS server running at http://localhost:8080 ...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logr.Error("Server error: %v", err)
	}
}
