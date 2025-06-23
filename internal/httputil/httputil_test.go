package httputil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"message": "test"}

	WriteJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	expectedContentType := "application/json"
	if ct := w.Header().Get("Content-Type"); ct != expectedContentType {
		t.Errorf("expected Content-Type %s, got %s", expectedContentType, ct)
	}

	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Errorf("failed to unmarshal response: %v", err)
	}

	if result["message"] != "test" {
		t.Errorf("expected message 'test', got %s", result["message"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	errorMsg := "test error"

	WriteError(w, http.StatusBadRequest, errorMsg)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Errorf("failed to unmarshal response: %v", err)
	}

	if result["error"] != errorMsg {
		t.Errorf("expected error '%s', got %s", errorMsg, result["error"])
	}
}

func TestDecodeJSON_Success(t *testing.T) {
	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	testData := TestStruct{Name: "test", Value: 42}
	jsonData, _ := json.Marshal(testData)

	req := httptest.NewRequest("POST", "/test", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")

	var result TestStruct
	err := DecodeJSON(req, &result)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if result.Name != "test" || result.Value != 42 {
		t.Errorf("expected {Name: test, Value: 42}, got %+v", result)
	}
}

func TestDecodeJSON_UnknownFields(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	// JSON with unknown field
	jsonData := `{"name": "test", "unknown": "field"}`

	req := httptest.NewRequest("POST", "/test", strings.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")

	var result TestStruct
	err := DecodeJSON(req, &result)

	if err == nil {
		t.Error("expected error for unknown fields, got nil")
	}

	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("expected unknown field error, got %v", err)
	}
}

func TestDecodeJSON_SizeLimit(t *testing.T) {
	type TestStruct struct {
		Data string `json:"data"`
	}

	// Create JSON data larger than MaxRequestSize
	largeData := strings.Repeat("a", MaxRequestSize+1)
	testData := TestStruct{Data: largeData}
	jsonData, _ := json.Marshal(testData)

	req := httptest.NewRequest("POST", "/test", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")

	var result TestStruct
	err := DecodeJSON(req, &result)

	if err == nil {
		t.Error("expected error for oversized request, got nil")
	}
}

func TestDecodeJSON_InvalidJSON(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	// Invalid JSON
	jsonData := `{"name": "test"`

	req := httptest.NewRequest("POST", "/test", strings.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")

	var result TestStruct
	err := DecodeJSON(req, &result)

	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestDecodeJSON_EmptyBody(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest("POST", "/test", bytes.NewReader([]byte{}))
	req.Header.Set("Content-Type", "application/json")

	var result TestStruct
	err := DecodeJSON(req, &result)

	if err == nil {
		t.Error("expected error for empty body, got nil")
	}
}
