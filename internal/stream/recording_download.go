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
		filePath := filepath.Join(rm.dir, filename)
		cleanPath := filepath.Clean(filePath)

		// Additional security: Ensure the resolved path is still within the recordings directory
		if !strings.HasPrefix(cleanPath, rm.dir) {
			httputil.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		if _, err := os.Stat(cleanPath); err != nil {
			httputil.WriteError(w, http.StatusNotFound, "File not found")
			return
		}

		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Header().Set("Content-Type", "video/mp4")

		f, err := os.Open(cleanPath)
		if err != nil {
			httputil.WriteError(w, http.StatusNotFound, "File not found")
			return
		}
		defer f.Close()

		// Copy file to response (using io.Copy is efficient for large files)
		if _, err := io.Copy(w, f); err != nil {
			rm.Logger.Error("Failed to serve recording file %s: %v", filename, err)
		}
	}
}
