package stream

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ApiDownloadRecording serves a recording file for download
func ApiDownloadRecording(rm *RecordingManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.URL.Query().Get("filename")
		if filename == "" {
			http.Error(w, "Missing filename", http.StatusBadRequest)
			return
		}
		filePath := filepath.Join(rm.dir, filename)
		if _, err := os.Stat(filePath); err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Header().Set("Content-Type", "video/mp4")
		f, err := os.Open(filePath)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		defer f.Close()
		_, _ = io.Copy(w, f)
	}
}
