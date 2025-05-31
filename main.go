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
	relayMgr = stream.NewRelayManager()
	logr     = config.NewLogger()
)

func apiStartRelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InputURL  string `json:"input_url"`
		OutputURL string `json:"output_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}
	if err := relayMgr.StartRelay(req.InputURL, req.OutputURL); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func apiStopRelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InputURL  string `json:"input_url"`
		OutputURL string `json:"output_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}
	if err := relayMgr.StopRelay(req.InputURL, req.OutputURL); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

func apiRelayStatus(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(relayMgr.Status())
}

func apiExportRelays(w http.ResponseWriter, r *http.Request) {
	if err := relayMgr.ExportConfig("relay_config.json"); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=relay_config.json")
	data, _ := os.ReadFile("relay_config.json")
	w.Write(data)
}

func apiImportRelays(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "No file uploaded"})
		return
	}
	defer file.Close()
	f, err := os.Create("relay_config.json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}
	defer f.Close()
	io.Copy(f, file)
	if err := relayMgr.ImportConfig("relay_config.json"); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "imported"})
}

func main() {
	fs := http.FileServer(http.Dir("web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/api/relay/start", apiStartRelay)
	http.HandleFunc("/api/relay/stop", apiStopRelay)
	http.HandleFunc("/api/relay/status", apiRelayStatus)
	http.HandleFunc("/api/relay/export", apiExportRelays)
	http.HandleFunc("/api/relay/import", apiImportRelays)

	logr.Info("Go-MLS relay manager running at http://localhost:8080 ...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logr.Error("Server error: %v", err)
	}
}
