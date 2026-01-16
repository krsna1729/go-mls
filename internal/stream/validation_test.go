package stream

import (
	"testing"
)

func TestValidateInputURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Valid URLs
		{"rtmp", "rtmp://server/app/stream", false},
		{"rtmps", "rtmps://server/app/stream", false},
		{"rtsp", "rtsp://localhost:8554/stream", false},
		{"http", "http://example.com/stream.m3u8", false},
		{"https", "https://example.com/stream.m3u8", false},
		{"file", "file:///path/to/video.mp4", false},
		{"srt", "srt://localhost:9000", false},

		// Invalid URLs
		{"empty", "", true},
		{"no scheme", "server/stream", true},
		{"invalid scheme", "ftp://server/file", true},
		{"javascript", "javascript:alert(1)", true},
		{"data", "data:text/plain,hello", true},

		// Path traversal
		{"path traversal", "file://../../../etc/passwd", true},
		{"system path", "file:///etc/passwd", true},
		{"proc", "file:///proc/self/environ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInputURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateInputURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateOutputURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Valid URLs
		{"rtmp", "rtmp://server/app/stream", false},
		{"file", "file:///recordings/out.mp4", false},

		// Invalid URLs
		{"empty", "", true},
		{"rtsp not allowed for output", "rtsp://server/stream", true},
		{"http not allowed for output", "http://server/stream", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOutputURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOutputURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateInputName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names
		{"simple", "English", false},
		{"with numbers", "Stream123", false},
		{"with underscore", "my_stream", false},
		{"with hyphen", "my-stream", false},
		{"with period", "stream.live", false},

		// Invalid names
		{"empty", "", true},
		{"starts with underscore", "_stream", true},
		{"starts with hyphen", "-stream", true},
		{"contains space", "my stream", true},
		{"contains slash", "my/stream", true},
		{"path traversal", "stream/../etc", true},
		{"special chars", "stream;echo", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInputName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateInputName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeForFFmpeg(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal_url", "normal_url"},
		{"url$(whoami)", "urlwhoami)"},
		{"url`id`", "urlid"},
		{"url;rm -rf /", "urlrm -rf /"},
		{"url\nmalicious", "urlmalicious"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeForFFmpeg(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeForFFmpeg(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestValidatePresetName(t *testing.T) {
	tests := []struct {
		preset  string
		wantErr bool
	}{
		{"", false},        // Empty allowed
		{"YouTube", false}, // Valid preset
		{"InvalidPreset", true},
	}

	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			err := ValidatePresetName(tt.preset)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePresetName(%q) error = %v, wantErr %v", tt.preset, err, tt.wantErr)
			}
		})
	}
}
