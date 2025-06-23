package stream

import (
	"encoding/json"
	"go-mls/internal/logger"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApiStartRecording(t *testing.T) {
	// Setup test environment
	tempDir := t.TempDir()
	log := logger.NewLogger()
	relayMgr := NewRelayManager(log, tempDir)
	rm := NewRecordingManager(log, tempDir, relayMgr)
	defer rm.Shutdown()

	handler := ApiStartRecording(rm)

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
		shouldContain  string
	}{
		{
			name:           "Valid request",
			requestBody:    `{"name": "test", "source": "rtsp://example.com/stream"}`,
			expectedStatus: http.StatusOK,
			shouldContain:  "recording started",
		},
		{
			name:           "Missing name",
			requestBody:    `{"source": "rtsp://example.com/stream"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Name and source required",
		},
		{
			name:           "Missing source",
			requestBody:    `{"name": "test"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Name and source required",
		},
		{
			name:           "Empty name",
			requestBody:    `{"name": "", "source": "rtsp://example.com/stream"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Name and source required",
		},
		{
			name:           "Undefined name",
			requestBody:    `{"name": "undefined", "source": "rtsp://example.com/stream"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "cannot be 'undefined'",
		},
		{
			name:           "Undefined source",
			requestBody:    `{"name": "test", "source": "undefined"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "cannot be 'undefined'",
		},
		{
			name:           "Invalid JSON",
			requestBody:    `{"name": "test"`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Invalid request",
		},
		{
			name:           "Unknown fields",
			requestBody:    `{"name": "test", "source": "rtsp://example.com/stream", "unknown": "field"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/start-recording", strings.NewReader(tt.requestBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if !strings.Contains(w.Body.String(), tt.shouldContain) {
				t.Errorf("expected response to contain '%s', got '%s'", tt.shouldContain, w.Body.String())
			}
		})
	}
}

func TestApiStopRecording(t *testing.T) {
	// Setup test environment
	tempDir := t.TempDir()
	log := logger.NewLogger()
	relayMgr := NewRelayManager(log, tempDir)
	rm := NewRecordingManager(log, tempDir, relayMgr)
	defer rm.Shutdown()

	handler := ApiStopRecording(rm)

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
		shouldContain  string
	}{
		{
			name:           "Recording not found",
			requestBody:    `{"name": "test", "source": "rtsp://example.com/stream"}`,
			expectedStatus: http.StatusInternalServerError,
			shouldContain:  "no active recording",
		},
		{
			name:           "Missing name",
			requestBody:    `{"source": "rtsp://example.com/stream"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Name and source required",
		},
		{
			name:           "Missing source",
			requestBody:    `{"name": "test"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Name and source required",
		},
		{
			name:           "Undefined values",
			requestBody:    `{"name": "undefined", "source": "undefined"}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "cannot be 'undefined'",
		},
		{
			name:           "Invalid JSON",
			requestBody:    `{"name": "test"`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/stop-recording", strings.NewReader(tt.requestBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if !strings.Contains(w.Body.String(), tt.shouldContain) {
				t.Errorf("expected response to contain '%s', got '%s'", tt.shouldContain, w.Body.String())
			}
		})
	}
}

func TestApiListRecordings(t *testing.T) {
	// Setup test environment
	tempDir := t.TempDir()
	log := logger.NewLogger()
	relayMgr := NewRelayManager(log, tempDir)
	rm := NewRecordingManager(log, tempDir, relayMgr)
	defer rm.Shutdown()

	// Create a test recording file
	testFile := filepath.Join(tempDir, "test_recording.mp4")
	testData := []byte("test video data")
	if err := os.WriteFile(testFile, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Add a mock recording to the manager
	rm.mu.Lock()
	rm.recordings["test_key"] = &Recording{
		Name:      "test",
		Source:    "rtsp://example.com/stream",
		FilePath:  testFile,
		Filename:  "test_recording.mp4",
		FileSize:  int64(len(testData)),
		StartedAt: time.Now(),
		Active:    false,
	}
	rm.mu.Unlock()

	handler := ApiListRecordings(rm)

	req := httptest.NewRequest("GET", "/api/recordings", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var recordings []Recording
	if err := json.Unmarshal(w.Body.Bytes(), &recordings); err != nil {
		t.Errorf("failed to unmarshal response: %v", err)
	}

	if len(recordings) != 1 {
		t.Errorf("expected 1 recording, got %d", len(recordings))
	}

	if recordings[0].Name != "test" {
		t.Errorf("expected name 'test', got '%s'", recordings[0].Name)
	}

	if recordings[0].FileSize != int64(len(testData)) {
		t.Errorf("expected file size %d, got %d", len(testData), recordings[0].FileSize)
	}
}

func TestApiDeleteRecording(t *testing.T) {
	// Setup test environment
	tempDir := t.TempDir()
	log := logger.NewLogger()
	relayMgr := NewRelayManager(log, tempDir)
	rm := NewRecordingManager(log, tempDir, relayMgr)
	defer rm.Shutdown()

	// Create a test recording file
	testFile := filepath.Join(tempDir, "test_recording.mp4")
	testData := []byte("test video data")
	if err := os.WriteFile(testFile, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Add a mock recording to the manager
	rm.mu.Lock()
	rm.recordings["test_key"] = &Recording{
		Name:      "test",
		Source:    "rtsp://example.com/stream",
		FilePath:  testFile,
		Filename:  "test_recording.mp4",
		FileSize:  int64(len(testData)),
		StartedAt: time.Now(),
		Active:    false,
	}
	rm.mu.Unlock()

	handler := ApiDeleteRecording(rm)

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
		shouldContain  string
		checkFileGone  bool
	}{
		{
			name:           "Valid delete",
			requestBody:    `{"filename": "test_recording.mp4"}`,
			expectedStatus: http.StatusOK,
			shouldContain:  "recording deleted",
			checkFileGone:  true,
		},
		{
			name:           "Missing filename",
			requestBody:    `{}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Filename required",
		},
		{
			name:           "Empty filename",
			requestBody:    `{"filename": ""}`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Filename required",
		},
		{
			name:           "Invalid JSON",
			requestBody:    `{"filename": "test"`,
			expectedStatus: http.StatusBadRequest,
			shouldContain:  "Invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("DELETE", "/api/recording", strings.NewReader(tt.requestBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if !strings.Contains(w.Body.String(), tt.shouldContain) {
				t.Errorf("expected response to contain '%s', got '%s'", tt.shouldContain, w.Body.String())
			}

			if tt.checkFileGone {
				if _, err := os.Stat(testFile); !os.IsNotExist(err) {
					t.Errorf("expected file to be deleted, but it still exists")
				}
			}
		})
	}
}

func TestApiHandlers_ContentType(t *testing.T) {
	// Setup test environment
	tempDir := t.TempDir()
	log := logger.NewLogger()
	relayMgr := NewRelayManager(log, tempDir)
	rm := NewRecordingManager(log, tempDir, relayMgr)
	defer rm.Shutdown()

	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
		body    string
	}{
		{
			name:    "Start recording returns JSON",
			handler: ApiStartRecording(rm),
			method:  "POST",
			path:    "/api/start-recording",
			body:    `{"name": "test", "source": "rtsp://example.com/stream"}`,
		},
		{
			name:    "Stop recording returns JSON",
			handler: ApiStopRecording(rm),
			method:  "POST",
			path:    "/api/stop-recording",
			body:    `{"name": "test", "source": "rtsp://example.com/stream"}`,
		},
		{
			name:    "List recordings returns JSON",
			handler: ApiListRecordings(rm),
			method:  "GET",
			path:    "/api/recordings",
			body:    "",
		},
		{
			name:    "Delete recording returns JSON",
			handler: ApiDeleteRecording(rm),
			method:  "DELETE",
			path:    "/api/recording",
			body:    `{"filename": "nonexistent.mp4"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			w := httptest.NewRecorder()

			tt.handler(w, req)

			contentType := w.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("expected Content-Type 'application/json', got '%s'", contentType)
			}

			// Verify response is valid JSON
			if strings.Contains(tt.path, "/recordings") && tt.method == "GET" {
				// List recordings returns an array
				var result []interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Errorf("response is not valid JSON array: %v", err)
				}
			} else {
				// Other endpoints return objects
				var result map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Errorf("response is not valid JSON object: %v", err)
				}
			}
		})
	}
}
