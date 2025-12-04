package stream

import (
	"go-mls/internal/httputil"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ApiDownloadRecording serves a recording file for download with security checks
func ApiDownloadRecording(rm *RecordingManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.URL.Query().Get("filename")
		if filename == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing filename")
			return
		}

		// Security: Validate filename to prevent path traversal attacks
		if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid filename")
			return
		}

		// Ensure filename has valid extension
		if !strings.HasSuffix(strings.ToLower(filename), ".mp4") {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid file type")
			return
		}

		// Resolve and clean the file path
		fileToServe := filepath.Join(rm.recordingDir, filename)
		cleanPath := filepath.Clean(fileToServe)

		// Additional security: Ensure the resolved path is still within the recordings directory
		if !strings.HasPrefix(cleanPath, rm.recordingDir) {
			httputil.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		if _, err := os.Stat(cleanPath); err != nil {
			httputil.WriteError(w, http.StatusNotFound, "File not found")
			return
		}

		f, err := os.Open(cleanPath)
		if err != nil {
			if os.IsPermission(err) {
				httputil.WriteError(w, http.StatusForbidden, "Access denied")
			} else {
				httputil.WriteError(w, http.StatusNotFound, "File not found")
			}
			return
		}
		defer f.Close()

		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Header().Set("Content-Type", "video/mp4")

		// Copy file to response (using io.Copy is efficient for large files)
		if _, err := io.Copy(w, f); err != nil {
			rm.Logger.Error("File requested outside recording directory", "filename", filename, "expectedDir", rm.recordingDir)
		}
	}
}
