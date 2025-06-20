package stream

import (
	"context"
	"go-mls/internal/httputil"
	"net/http"
)

// Recording API Handlers
func ApiStartRecording(rm *RecordingManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.Name == "" || req.Source == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name and source required")
			return
		}
		// Additional validation to prevent "undefined" values
		if req.Name == "undefined" || req.Source == "undefined" {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid name or source: cannot be 'undefined'")
			return
		}
		if err := rm.StartRecording(context.Background(), req.Name, req.Source); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "recording started"})
	}
}

func ApiStopRecording(rm *RecordingManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.Name == "" || req.Source == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name and source required")
			return
		}
		// Additional validation to prevent "undefined" values
		if req.Name == "undefined" || req.Source == "undefined" {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid name or source: cannot be 'undefined'")
			return
		}
		if err := rm.StopRecording(req.Name, req.Source); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "recording stopped"})
	}
}

func ApiListRecordings(rm *RecordingManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recs := rm.ListRecordings()
		httputil.WriteJSON(w, http.StatusOK, recs)
	}
}

func ApiDeleteRecording(rm *RecordingManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Filename string `json:"filename"`
		}
		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if req.Filename == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Filename required")
			return
		}
		if err := rm.DeleteRecordingByFilename(req.Filename); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "recording deleted"})
	}
}
