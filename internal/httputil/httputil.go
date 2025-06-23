package httputil

import (
	"encoding/json"
	"io"
	"net/http"
)

// MaxRequestSize is the maximum allowed request body size (1MB)
const MaxRequestSize = 1 << 20 // 1MB

// WriteJSON writes a JSON response with the given status code
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// WriteError writes a JSON error response
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// DecodeJSON decodes JSON from request body into v with size limit protection
func DecodeJSON(r *http.Request, v interface{}) error {
	// Limit request body size to prevent DoS attacks
	limitedReader := io.LimitReader(r.Body, MaxRequestSize)
	defer r.Body.Close()

	decoder := json.NewDecoder(limitedReader)
	decoder.DisallowUnknownFields() // Reject unknown fields for security
	return decoder.Decode(v)
}
