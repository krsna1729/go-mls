package config

import (
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
	if config.Relay.InputTimeout != 30*time.Second {
		t.Errorf("expected input timeout 30s, got %v", config.Relay.InputTimeout)
	}

	if config.Relay.OutputTimeout != 60*time.Second {
		t.Errorf("expected output timeout 60s, got %v", config.Relay.OutputTimeout)
	}

	// Test Recording defaults
	if config.Recording.Directory != "recordings" {
		t.Errorf("expected recording directory 'recordings', got '%s'", config.Recording.Directory)
	}
}

func TestLoadConfigNonExistent(t *testing.T) {
	config, err := LoadConfig("nonexistent.json")
	if err != nil {
		t.Errorf("expected no error loading nonexistent config, got %v", err)
	}

	// Should return default config
	if config.HTTP.Port != "8080" {
		t.Errorf("expected default port, got %s", config.HTTP.Port)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test_config.json")

	// Create a custom config
	config := DefaultConfig()
	config.HTTP.Port = "9090"
	config.Relay.InputTimeout = 45 * time.Second
	config.Recording.Directory = "/custom/recordings"

	// Save config
	err := config.SaveConfig(configFile)
	if err != nil {
		t.Errorf("failed to save config: %v", err)
	}

	// Load config
	loadedConfig, err := LoadConfig(configFile)
	if err != nil {
		t.Errorf("failed to load config: %v", err)
	}

	// Verify values
	if loadedConfig.HTTP.Port != "9090" {
		t.Errorf("expected port '9090', got '%s'", loadedConfig.HTTP.Port)
	}

	if loadedConfig.Relay.InputTimeout != 45*time.Second {
		t.Errorf("expected input timeout 45s, got %v", loadedConfig.Relay.InputTimeout)
	}

	if loadedConfig.Recording.Directory != "/custom/recordings" {
		t.Errorf("expected directory '/custom/recordings', got '%s'", loadedConfig.Recording.Directory)
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
				c.Relay.InputTimeout = 60 * time.Second
				c.Relay.OutputTimeout = 30 * time.Second
			},
			shouldError: true,
			errorMsg:    "output timeout must be greater than input timeout",
		},
		{
			name: "Invalid RTSP port",
			modifyFunc: func(c *Config) {
				c.Relay.RTSPServer.Port = 0
			},
			shouldError: true,
			errorMsg:    "RTSP server port must be between 1 and 65535",
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
	config.Relay.RTSPServer.Host = "192.168.1.100"
	config.Relay.RTSPServer.Port = 8554

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

	_, err = LoadConfig(configFile)
	if err == nil {
		t.Error("expected error loading invalid JSON, got nil")
	}
}

func TestLoadConfigInvalidValues(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "invalid_values.json")

	// Write config with invalid values
	invalidConfig := `{
		"http": {
			"host": "0.0.0.0",
			"port": ""
		},
		"relay": {
			"input_timeout": "30s",
			"output_timeout": "60s"
		}
	}`

	err := os.WriteFile(configFile, []byte(invalidConfig), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err = LoadConfig(configFile)
	if err == nil {
		t.Error("expected validation error, got nil")
	}
}
