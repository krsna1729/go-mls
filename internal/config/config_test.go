package config

import (
	"go-mls/internal/logger"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Test HTTP defaults
	if config.HTTP.Host != "0.0.0.0" {
		t.Errorf("expected HTTP host '0.0.0.0', got '%s'", config.HTTP.Host)
	}

	if config.HTTP.Port != "8080" {
		t.Errorf("expected HTTP port '8080', got '%s'", config.HTTP.Port)
	}

	// Test Relay defaults
	if time.Duration(config.Relay.InputTimeout) != 30*time.Second {
		t.Errorf("expected default input timeout 30s, got %v", config.Relay.InputTimeout)
	}
	if time.Duration(config.Relay.OutputTimeout) != 60*time.Second {
		t.Errorf("expected default output timeout 60s, got %v", config.Relay.OutputTimeout)
	}
	if config.Relay.HubType != "rtmp" {
		t.Errorf("expected default hub_type 'rtmp', got '%s'", config.Relay.HubType)
	}
	if config.Relay.RTMPHub.Host != "0.0.0.0" || config.Relay.RTMPHub.Port != 1935 {
		t.Errorf("expected default RTMP hub 0.0.0.0:1935, got %s:%d", config.Relay.RTMPHub.Host, config.Relay.RTMPHub.Port)
	}
	if config.Relay.RTSPHub.Host != "0.0.0.0" || config.Relay.RTSPHub.Port != 8554 {
		t.Errorf("expected default RTSP hub 0.0.0.0:8554, got %s:%d", config.Relay.RTSPHub.Host, config.Relay.RTSPHub.Port)
	}

	// Test Recording defaults
	if config.Recording.Directory != "recordings" {
		t.Errorf("expected recording directory 'recordings', got '%s'", config.Recording.Directory)
	}
	if config.HLS.PlaylistBaseDir != "/tmp" {
		t.Errorf("expected default hls dir '/tmp', got '%s'", config.HLS.PlaylistBaseDir)
	}
	if config.FFmpeg.Path != "ffmpeg" || config.FFmpeg.LogLevel != "error" {
		t.Errorf("expected default ffmpeg config path=ffmpeg loglevel=error, got path=%s loglevel=%s", config.FFmpeg.Path, config.FFmpeg.LogLevel)
	}
}

func TestLoadConfigNonExistent(t *testing.T) {
	config, err := LoadConfig("nonexistent.json", logger.NewLogger())
	if err != nil {
		t.Errorf("expected no error loading nonexistent config, got %v", err)
	}

	// Should return default config
	if config.HTTP.Port != "8080" {
		t.Errorf("expected default port, got %s", config.HTTP.Port)
	}
	if config.Relay.HubType != "rtmp" {
		t.Errorf("expected default hub_type, got %s", config.Relay.HubType)
	}
	if config.HLS.PlaylistBaseDir != "/tmp" {
		t.Errorf("expected default hls playlist_base_dir '/tmp', got %s", config.HLS.PlaylistBaseDir)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		modifyFunc  func(*Config)
		shouldError bool
		errorMsg    string
	}{
		{
			name: "Valid config",
			modifyFunc: func(c *Config) {
				// Keep defaults
			},
			shouldError: false,
		},
		{
			name: "Empty HTTP port",
			modifyFunc: func(c *Config) {
				c.HTTP.Port = ""
			},
			shouldError: true,
			errorMsg:    "HTTP port cannot be empty",
		},
		{
			name: "Zero input timeout",
			modifyFunc: func(c *Config) {
				c.Relay.InputTimeout = 0
			},
			shouldError: true,
			errorMsg:    "input timeout must be positive",
		},
		{
			name: "Output timeout not greater than input",
			modifyFunc: func(c *Config) {
				c.Relay.InputTimeout = Duration(60 * time.Second)
				c.Relay.OutputTimeout = Duration(30 * time.Second)
			},
			shouldError: true,
			errorMsg:    "output timeout must be greater than input timeout",
		},
		{
			name: "Invalid RTMP hub port",
			modifyFunc: func(c *Config) {
				c.Relay.RTMPHub.Port = 0
			},
			shouldError: true,
			errorMsg:    "RTMP hub port must be between 1 and 65535",
		},
		{
			name: "Invalid hub type",
			modifyFunc: func(c *Config) {
				c.Relay.HubType = "invalid"
			},
			shouldError: true,
			errorMsg:    "hub_type must be 'rtmp' or 'rtsp'",
		},
		{
			name: "Empty recording directory",
			modifyFunc: func(c *Config) {
				c.Recording.Directory = ""
			},
			shouldError: true,
			errorMsg:    "recording directory cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			tt.modifyFunc(config)

			err := config.Validate()

			if tt.shouldError {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("expected error '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestGetRTSPServerURL(t *testing.T) {
	config := DefaultConfig()
	config.Relay.RTSPHub.Host = "192.168.1.100"
	config.Relay.RTSPHub.Port = 8554

	expected := "rtsp://192.168.1.100:8554"
	actual := config.GetRTSPServerURL()

	if actual != expected {
		t.Errorf("expected RTSP URL '%s', got '%s'", expected, actual)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "invalid.json")

	// Write invalid JSON
	err := os.WriteFile(configFile, []byte(`{"http": port: 8080}`), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err = LoadConfig(configFile, logger.NewLogger())
	if err == nil {
		t.Error("expected error loading invalid JSON, got nil")
	}
}

func TestLoadConfigInvalidValues(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "invalid_values.json")

	invalidConfig := `{
		"relay": {
			"hub_type": "invalid"
		}
	}`

	err := os.WriteFile(configFile, []byte(invalidConfig), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err = LoadConfig(configFile, logger.NewLogger())
	if err == nil {
		t.Error("expected validation error, got nil")
	}
}

func TestLoadConfig_ReadError(t *testing.T) {
	// Try to load from a file that cannot be read (simulate permission error)
	file := filepath.Join(t.TempDir(), "no_read.json")
	os.WriteFile(file, []byte(`{}`), 0000) // no permissions
	_, err := LoadConfig(file, logger.NewLogger())
	if err == nil {
		t.Error("expected error reading file, got nil")
	}
}

func TestLoadConfig_ParseError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(file, []byte(`notjson`), 0644)
	_, err := LoadConfig(file, logger.NewLogger())
	if err == nil {
		t.Error("expected parse error, got nil")
	}
}

func TestLoadConfig_ValidationError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "badval.json")
	os.WriteFile(file, []byte(`{"relay": {"hub_type": "broken"}}`), 0644)
	_, err := LoadConfig(file, logger.NewLogger())
	if err == nil {
		t.Error("expected validation error, got nil")
	}
}

func TestDuration_UnmarshalJSON_InvalidString(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte(`"12min"`))
	if err == nil {
		t.Error("expected error for invalid duration string, got nil")
	}
}

func TestDuration_UnmarshalJSON_NonString(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte(`123`)) // not a string
	if err == nil {
		t.Error("expected error for non-string JSON, got nil")
	}
}

func TestLoadConfig_BadDurations(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "bad_duration.json")
	badConfig := `{
		"http": {
			"host": "0.0.0.0",
			"port": "8080",
			"read_timeout": "12min",
			"write_timeout": "30s",
			"idle_timeout": "120s"
		},
		"relay": {
			"input_timeout": "30s",
			"output_timeout": "60s",
			"rtsp_hub": {"host": "127.0.0.1", "port": 8554}
		},
		"recording": {"directory": "recordings"},
		"logging": {"level": "info"},
		"hls": {},
		"ffmpeg": {"path": "ffmpeg", "loglevel": "info"}
	}`
	os.WriteFile(file, []byte(badConfig), 0644)
	_, err := LoadConfig(file, logger.NewLogger())
	if err == nil {
		t.Error("expected error for bad duration string, got nil")
	}
}

func TestLoadConfig_BadTypes(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "bad_types.json")
	badConfig := `{
		"http": {
			"host": "0.0.0.0",
			"port": 8080,
			"read_timeout": "30s",
			"write_timeout": "30s",
			"idle_timeout": "120s"
		},
		"relay": {
			"input_timeout": "30s",
			"output_timeout": "60s",
			"rtsp_hub": {"host": "127.0.0.1", "port": 8554}
		},
		"recording": {"directory": "recordings"},
		"logging": {"level": "info"},
		"hls": {},
		"ffmpeg": {"path": "ffmpeg", "loglevel": "info"}
	}`
	os.WriteFile(file, []byte(badConfig), 0644)
	_, err := LoadConfig(file, logger.NewLogger())
	if err == nil {
		t.Error("expected error for bad port type, got nil")
	}
}
