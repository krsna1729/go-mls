// Package api provides HTTP health check handlers for monitoring.
package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"go-mls/internal/stream"
)

// BuildInfo contains version and build information.
// These should be set at build time using ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// VersionInfo is returned by the /version endpoint.
type VersionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

// HealthStatus is returned by the /health endpoint.
type HealthStatus struct {
	Status    string `json:"status"` // "healthy" or "unhealthy"
	Timestamp string `json:"timestamp"`
}

// ReadinessStatus is returned by the /ready endpoint.
type ReadinessStatus struct {
	Status     string            `json:"status"` // "ready" or "not_ready"
	Timestamp  string            `json:"timestamp"`
	Components map[string]string `json:"components"`
}

// HealthHandler returns a handler for liveness probe.
// Returns 200 if the server is running.
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		status := HealthStatus{
			Status:    "healthy",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(status)
	}
}

// ReadinessHandler returns a handler for readiness probe.
// Checks that critical dependencies are available.
func ReadinessHandler(rtsp *stream.RTSPServerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		components := make(map[string]string)
		allReady := true

		// Check FFmpeg availability
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			components["ffmpeg"] = "not found in PATH"
			allReady = false
		} else {
			components["ffmpeg"] = "available"
		}

		// Check RTSP server
		if rtsp != nil {
			stats := rtsp.GetStreamStats()
			if stats == nil {
				components["rtsp_server"] = "not running"
				allReady = false
			} else {
				components["rtsp_server"] = "running"
			}
		} else {
			components["rtsp_server"] = "not configured"
			allReady = false
		}

		status := ReadinessStatus{
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			Components: components,
		}

		if allReady {
			status.Status = "ready"
			w.WriteHeader(http.StatusOK)
		} else {
			status.Status = "not_ready"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	}
}

// VersionHandler returns a handler for the /version endpoint.
func VersionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		info := VersionInfo{
			Version:   Version,
			GitCommit: GitCommit,
			BuildDate: BuildDate,
			GoVersion: runtime.Version(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(info)
	}
}
