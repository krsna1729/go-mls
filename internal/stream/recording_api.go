package stream

import (
	"context"
	"go-mls/internal/httputil"
	"net/http"
	"os"
	"path/filepath"
)

func ApiStartRecording(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name      string `json:"name"`
			InputName string `json:"input_name"`
			InputURL  string `json:"input_url"`
			Source    string `json:"source"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required")
			return
		}

		inputName := req.InputName
		if inputName == "" {
			inputName = req.Source
		}

		if inputName == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Input name is required")
			return
		}

		inputURL := req.InputURL
		if inputURL == "" {
			inputURL = req.Source
		}

		ctx := context.Background()

		if _, exists := p.GetInputURL(inputName); !exists {
			if inputURL == "" {
				httputil.WriteError(w, http.StatusBadRequest, "Input URL is required when input doesn't exist")
				return
			}
			if err := p.StartInput(ctx, inputName, inputURL); err != nil {
				p.Logger.Error("ApiStartRecording: failed to start input", "err", err)
				httputil.WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		if err := p.StartRecording(ctx, req.Name, inputName); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
	}
}

func ApiStopRecording(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required")
			return
		}

		if err := p.StopRecording(req.Name); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

func ApiListRecordings(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recordings := p.ListRecordings()
		httputil.WriteJSON(w, http.StatusOK, recordings)
	}
}

func ApiDeleteRecording(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Filename string `json:"filename"`
		}

		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request")
			return
		}

		if req.Filename == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Filename is required")
			return
		}

		if err := p.DeleteRecording(req.Filename); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func ApiDownloadRecording(p *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.URL.Query().Get("filename")
		if filename == "" {
			http.Error(w, "Filename is required", http.StatusBadRequest)
			return
		}

		filePath := filepath.Join(p.GetRecDir(), filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		http.ServeFile(w, r, filePath)
	}
}

func ApiRecordingsSSE(p *Pipeline) http.HandlerFunc {
	return p.SSE.Handler()
}
